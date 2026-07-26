package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	var (
		transportFlag string
		portFlag      string
		authTokenFlag string
		langFlag      string
		archFlag      string
	)

	flag.StringVar(&transportFlag, "transport", "stdio", "MCP transport: stdio or http")
	flag.StringVar(&portFlag, "port", "8080", "HTTP port when running with -transport http")
	flag.StringVar(&authTokenFlag, "auth-token", "", "Optional bearer auth token for HTTP transport mode (e.g. -auth-token secret123)")
	flag.StringVar(&langFlag, "language", "en_us", "Default STT model language")
	flag.StringVar(&archFlag, "arch", "tiny", "Default STT model architecture")
	flag.Parse()

	mcpSvc := NewMCPServer(langFlag, archFlag)
	defer mcpSvc.Close()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	switch transportFlag {
	case "stdio":
		fmt.Fprintf(os.Stderr, "[samples/mcp-transcribe] Starting MCP server over stdio...\n")
		if err := mcpSvc.mcpServer.Run(ctx, &mcp.StdioTransport{}); err != nil {
			fmt.Fprintf(os.Stderr, "Error running stdio MCP server: %v\n", err)
			os.Exit(1)
		}

	case "http":
		fmt.Fprintf(os.Stderr, "[samples/mcp-transcribe] Starting MCP server over Streamable HTTP on :%s...\n", portFlag)
		mcpHandler := mcp.NewStreamableHTTPHandler(
			func(req *http.Request) *mcp.Server {
				return mcpSvc.mcpServer
			},
			nil,
		)

		var finalHandler http.Handler = mcpHandler
		if authTokenFlag != "" {
			fmt.Fprintf(os.Stderr, "[samples/mcp-transcribe] Bearer token auth enabled for HTTP endpoint.\n")
			finalHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				authHeader := r.Header.Get("Authorization")
				queryToken := r.URL.Query().Get("token")

				valid := false
				if strings.HasPrefix(authHeader, "Bearer ") && strings.TrimPrefix(authHeader, "Bearer ") == authTokenFlag {
					valid = true
				} else if queryToken != "" && queryToken == authTokenFlag {
					valid = true
				}

				if !valid {
					http.Error(w, "401 Unauthorized: invalid or missing Bearer token", http.StatusUnauthorized)
					return
				}

				mcpHandler.ServeHTTP(w, r)
			})
		}

		server := &http.Server{
			Addr:    ":" + portFlag,
			Handler: finalHandler,
		}

		go func() {
			<-ctx.Done()
			_ = server.Shutdown(context.Background())
		}()

		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Fprintf(os.Stderr, "HTTP server error: %v\n", err)
			os.Exit(1)
		}

	default:
		fmt.Fprintf(os.Stderr, "Unknown transport %q (supported: stdio, http)\n", transportFlag)
		os.Exit(1)
	}
}
