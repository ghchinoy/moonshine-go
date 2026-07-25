// Command grpc-listen is the smallest possible gRPC consumer of `moonshine
// serve`'s live transcript feed: it dials the gRPC endpoint (:9090 by default),
// opens a VoiceSidecar.Stream bidirectional gRPC stream, and prints each
// newly-finalized line as it arrives.
//
// Compare with ../go-listen, which speaks WebSocket/JSON instead. Both samples
// do the exact same Tier 0 job (subscribe to live finalized lines), but this
// sample uses the strongly-typed protobuf contract in pkg/servepb rather than
// hand-decoding JSON frames off a WebSocket.
//
// Usage:
//
//	moonshine serve --transport grpc --grpc-addr :9090
//	go run . -addr :9090
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/ghchinoy/moonshine-go/pkg/servepb"
)

func main() {
	addr := flag.String("addr", ":9090", "moonshine serve gRPC server address (host:port)")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	conn, err := grpc.NewClient(*addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("creating gRPC client for %s: %v", *addr, err)
	}
	defer conn.Close() //nolint:errcheck

	client := servepb.NewVoiceSidecarClient(conn)
	stream, err := client.Stream(ctx)
	if err != nil {
		log.Fatalf("opening gRPC stream to %s: %v (is `moonshine serve --transport grpc` running?)", *addr, err)
	}

	fmt.Printf("connected to gRPC %s -- listening for finalized transcript lines (Ctrl-C to stop)\n\n", *addr)

	seen := make(map[uint64]bool)
	for {
		ev, err := stream.Recv()
		if err != nil {
			if ctx.Err() != nil {
				fmt.Println("\nstopped.")
				return
			}
			log.Fatalf("receiving gRPC frame: %v", err)
		}

		te := ev.GetTranscript()
		if te == nil {
			continue // display card or action_result frame -- not this sample's concern
		}

		// Look up lines by ID for newly finalized line IDs. Honors the same
		// idempotency contract as WebSocket subscribers (see docs/serve-sidecar.md):
		// interim updates may drop under backpressure, but a finalized line
		// ID is handled exactly once.
		byID := make(map[uint64]*servepb.Line, len(te.GetLines()))
		for _, l := range te.GetLines() {
			byID[l.GetId()] = l
		}

		for _, id := range te.GetFinalizedLineIds() {
			if seen[id] {
				continue
			}
			seen[id] = true
			if l, ok := byID[id]; ok {
				fmt.Printf("[FINAL] %s\n", l.GetText())
			}
		}
	}
}
