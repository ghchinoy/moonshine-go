package serveapi

// AudioSource supplies mono float32 PCM audio to a transcription session. It
// abstracts over where audio comes from: the local microphone, a file or
// stream, or -- the motivating case -- a remote client (e.g. a browser that
// captures the mic with getUserMedia and streams PCM over WebSocket).
//
// Samples must be mono float32 at the transcriber's expected sample rate
// (16 kHz for the current Moonshine models).
//
// # Backpressure is the implementation's responsibility
//
// Unlike transcript fan-out (where the sidecar deliberately drops interim
// updates for slow subscribers), an AudioSource must NOT drop audio: dropping
// inbound samples corrupts the transcript. Implementations that wrap a producer
// which can outrun the consumer (a bursting or unreliable network client) must
// apply their own bounded-buffer / flow-control strategy. Do not assume the
// caller will tolerate lost frames.
//
// # Lifecycle
//
// The channel returned by Chunks is closed when the source is done -- either a
// clean end of stream or an abnormal termination such as a dropped connection.
// A closed channel alone cannot distinguish the two, so after the channel
// closes the consumer should call Err: a nil result means clean EOF, a non-nil
// result means the source terminated abnormally and the session should surface
// or react to that error rather than treating it as a normal end. (This mirrors
// the bufio.Scanner.Err convention.)
type AudioSource interface {
	// Chunks returns a channel of mono float32 PCM buffers. It is closed when
	// the source is finished. Implementations own their own backpressure and
	// must not drop audio.
	Chunks() <-chan []float32

	// Err returns a non-nil error if the source terminated abnormally. It
	// should be called only after the Chunks channel is closed; before then
	// its result is unspecified. A nil result after close means clean EOF.
	Err() error
}
