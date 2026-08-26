package main

import (
	"context"
	"errors"
	"log"
	"os"
	"os/signal"

	"github.com/MorenoLand/Moreno.ComputerUseMCP/internal/activity"
	"github.com/MorenoLand/Moreno.ComputerUseMCP/internal/computer"
	"github.com/MorenoLand/Moreno.ComputerUseMCP/internal/platform"
	"github.com/MorenoLand/Moreno.ComputerUseMCP/internal/server"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	log.SetFlags(0)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	backend := platform.New()
	defer backend.Close()
	service := computer.NewService(backend)
	controller := activity.New(service.Abort)
	defer controller.Close()
	if err := server.New(service, controller).Run(ctx, &mcp.StdioTransport{}); err != nil && !errors.Is(ctx.Err(), context.Canceled) {
		log.Fatal(err)
	}
}
