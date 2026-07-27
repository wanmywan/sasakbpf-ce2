package config

func xorDecode(encoded, key []byte) string {
	if len(encoded) == 0 || len(key) == 0 {
		return ""
	}
	out := make([]byte, len(encoded))
	for i := range encoded {
		out[i] = encoded[i] ^ key[i%len(key)]
	}
	return string(out)
}
