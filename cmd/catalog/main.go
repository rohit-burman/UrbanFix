package main

import (
	"context"
	"log"
	"net"
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
	catalogpb "pro.com/urbanfix/proto"
)

type service struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type serviceRequest struct {
	Name string `json:"name"`
}

type catalogServer struct {
	catalogpb.UnimplementedCatalogServiceServer

	pool *pgxpool.Pool
}

func (s *catalogServer) CheckServiceExists(ctx context.Context, req *catalogpb.CheckServiceRequest) (*catalogpb.CheckServiceResponse, error) {
	var exists bool

	err := s.pool.QueryRow(ctx,
		"SELECT EXISTS (SELECT 1 FROM services WHERE id = $1)",
		req.GetServiceId(),
	).Scan(&exists)

	if err != nil {
		return nil, err
	}

	return &catalogpb.CheckServiceResponse{Exists: exists}, nil
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

	app := fiber.New()

	app.Get("/services", func(c fiber.Ctx) error {
		rows, err := pool.Query(context.Background(), "SELECT id, name FROM services")
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "db error"})
		}

		defer rows.Close()

		list := []service{}

		for rows.Next() {
			var s service

			if err := rows.Scan(&s.ID, &s.Name); err != nil {
				return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "db error"})
			}
			list = append(list, s)
		}

		if err := rows.Err(); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "db error"})
		}

		return c.JSON(list)
	})

	app.Post("/services", func(c fiber.Ctx) error {
		var body serviceRequest

		if err := c.Bind().Body(&body); err != nil || strings.TrimSpace(body.Name) == "" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Name is required"})
		}

		var s service
		err := pool.QueryRow(context.Background(),
			"INSERT INTO services (name) VALUES ($1) RETURNING id, name",
			strings.TrimSpace(body.Name),
		).Scan(&s.ID, &s.Name)

		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "db error"})
		}

		return c.Status(fiber.StatusCreated).JSON(s)

	})

	// opening a tcp listner on a diff port
	lis, err := net.Listen("tcp", ":50051")
	if err != nil {
		log.Fatal(err)
	}

	grpcServer := grpc.NewServer()
	catalogpb.RegisterCatalogServiceServer(grpcServer, &catalogServer{pool: pool})

	go func() {
		log.Fatal(grpcServer.Serve(lis))
	}()

	log.Fatal(app.Listen(":8000"))

}
