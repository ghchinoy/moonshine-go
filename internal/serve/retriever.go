package serve

import (
	"github.com/ghchinoy/moonshine-go/pkg/serveapi"
)

// Retriever, Result, NoopRetriever, and StaticRetriever are aliases of the
// identically-named pkg/serveapi types: the public package is the source of
// truth (and what external Tier-2 Go consumers implement Retriever against).
type (
	Retriever       = serveapi.Retriever
	Result          = serveapi.Result
	NoopRetriever   = serveapi.NoopRetriever
	StaticRetriever = serveapi.StaticRetriever
)

// NewStaticRetriever creates a StaticRetriever pre-populated with items.
func NewStaticRetriever(items ...Result) *StaticRetriever {
	return serveapi.NewStaticRetriever(items...)
}
