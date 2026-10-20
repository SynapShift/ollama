package cmd

import (
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSynthesize(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Input, Voice string }
		if r.URL.Path != "/v1/audio/speech" || json.NewDecoder(r.Body).Decode(&req) != nil || req.Input == "" || req.Voice != "Toby" {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`{"error":{"message":"input is required"}}`))
			return
		}
		wav := make([]byte, 48)
		binary.LittleEndian.PutUint32(wav[4:], 0xffffffff)
		binary.LittleEndian.PutUint32(wav[40:], 0xffffffff)
		copy(wav[44:], []byte{1, 0, 2, 0})
		w.Write(wav)
	}))
	defer srv.Close()
	t.Setenv("OLLAMA_HOST", srv.URL)

	wav, err := synthesize(t.Context(), "phonon", "Toby", "hello")
	if err != nil || binary.LittleEndian.Uint32(wav[4:]) != 40 || binary.LittleEndian.Uint32(wav[40:]) != 4 {
		t.Fatalf("WAV lengths not recorded: %x, %v", wav, err)
	}
	if _, err := synthesize(t.Context(), "phonon", "Toby", ""); err == nil || err.Error() != "input is required" {
		t.Fatalf("server error lost: %v", err)
	}
}
