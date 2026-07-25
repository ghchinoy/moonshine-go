package moonshine

// Option is a name/value pair passed to the moonshine C API, mirroring
// struct moonshine_option_t. See moonshine-c-api.h for the recognized
// option names for each call (e.g. "voice", "speed", "g2p_root",
// "model_root", "ort_providers", "identify_speakers", ...).
type Option struct {
	Name  string
	Value string
}

// Options is a convenience constructor: Options("voice", "kokoro_af_heart",
// "speed", "1.0") builds a []Option from alternating name/value strings.
func Options(kv ...string) []Option {
	if len(kv)%2 != 0 {
		panic("moonshine: Options() requires an even number of name/value strings")
	}
	out := make([]Option, 0, len(kv)/2)
	for i := 0; i < len(kv); i += 2 {
		out = append(out, Option{Name: kv[i], Value: kv[i+1]})
	}
	return out
}
