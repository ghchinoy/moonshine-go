package serve

import (
	"github.com/ghchinoy/moonshine-go/pkg/serveapi"
)

// AudioEncoding, AudioFormat, and RemoteAudioSource are aliases of the
// public pkg/serveapi types.
type (
	AudioEncoding     = serveapi.AudioEncoding
	AudioFormat       = serveapi.AudioFormat
	RemoteAudioSource = serveapi.RemoteAudioSource
)

const (
	AudioEncodingFloat32 = serveapi.AudioEncodingFloat32
	AudioEncodingInt16   = serveapi.AudioEncodingInt16
)

// NewRemoteAudioSource creates a RemoteAudioSource pre-configured with format.
func NewRemoteAudioSource(format AudioFormat, bufferSize int) *RemoteAudioSource {
	return serveapi.NewRemoteAudioSource(format, bufferSize)
}
