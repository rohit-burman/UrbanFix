package main

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/jackc/pgx/v5/pgxpool"
	amqp "github.com/rabbitmq/amqp091-go"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	catalogpb "pro.com/urbanfix/proto"
)

type booking struct {
	ID           int64     `json:"id"`
	ServiceID    int64     `json:"service_id"`
	CustomerName string    `json:"customer_name"`
	Status       string    `json:"status"`
	CreatedAt    time.Time `json:"created_at"`
}

type bookingRequest struct {
	ServiceID    int64  `json:"service_id"`
	CustomerName string `json:"customer_name"`
}

type bookingCreated struct {
	BookingID    int64  `json:"id"`
	ServiceID    int64  `json:"service_id"`
	CustomerName string `json:"customer_name"`
}

func main() {
	pool, err := pgxpool.New(context.Background(), "postgres://urbanfix:urbanfix@localhost:5432/urbanfix?sslmode=disable")
	if err != nil {
		log.Fatal(err)
	}

	defer pool.Close()

	if err := pool.Ping(context.Background()); err != nil {
		log.Fatal(err)
	}

	conn, err := grpc.NewClient("localhost:50051", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()

	catalog := catalogpb.NewCatalogServiceClient(conn)

	amqpConn, err := amqp.Dial("amqp://urbanfix:urbanfix@localhost:5672/")
	if err != nil {
		log.Fatal(err)
	}
	defer amqpConn.Close()

	ch, err := amqpConn.Channel()
	if err != nil {
		log.Fatal(err)
	}
	defer ch.Close()

	_, err = ch.QueueDeclare("booking.created", true, false, false, false, nil)

	if err != nil {
		log.Fatal(err)
	}

	app := fiber.New()

	app.Post("/bookings", func(c fiber.Ctx) error {
		var body bookingRequest
		if err := c.Bind().Body(&body); err != nil || body.ServiceID <= 0 || strings.TrimSpace(body.CustomerName) == "" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "service_id  and customer_name are reqired or wrong data"})
		}

		// grpc  call : booking  -> catalog
		// unary rpc

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		resp, err := catalog.CheckServiceExists(ctx, &catalogpb.CheckServiceRequest{ServiceId: body.ServiceID})

		if err != nil {
			return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "catalog service unavailable"})
		}

		if !resp.GetExists() {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "service not found"})
		}

		var b booking

		err = pool.QueryRow(context.Background(),
			"INSERT INTO bookings (service_id, customer_name) VALUES ($1,$2) RETURNING id,service_id,customer_name,status,created_at",
			body.ServiceID, body.CustomerName,
		).Scan(&b.ID, &b.ServiceID, &b.CustomerName, &b.Status, &b.CreatedAt)

		payload, err := json.Marshal(bookingCreated{
			BookingID:    b.ID,
			ServiceID:    b.ServiceID,
			CustomerName: b.CustomerName,
		})

		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "db error"})
		}

		err = ch.PublishWithContext(context.Background(), "", "booking.created", false, false, amqp.Publishing{
			ContentType:  "application/json",
			DeliveryMode: amqp.Persistent,
			Body:         payload,
		})
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "rabbit error"})
		}

		return c.Status(fiber.StatusCreated).JSON(b)

	})

	app.Get("/bookings", func(c fiber.Ctx) error {
		rows, err := pool.Query(context.Background(),
			"SELECT * FROM bookings ORDER BY id")
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "db error"})
		}
		defer rows.Close()

		list := []booking{}
		for rows.Next() {
			var b booking
			if err := rows.Scan(&b.ID, &b.ServiceID, &b.CustomerName, &b.Status, &b.CreatedAt); err != nil {
				return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "db error"})
			}
			list = append(list, b)
		}
		if err := rows.Err(); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "db error"})

		}
		return c.JSON(list)
	})
	log.Fatal(app.Listen(":8001"))

}
