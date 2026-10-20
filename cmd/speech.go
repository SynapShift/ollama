package cmd

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"

	"golang.org/x/term"

	"github.com/ollama/ollama/envconfig"
	"github.com/ollama/ollama/readline"
)

// runSpeech reads text aloud, or reads each line typed at the prompt aloud.
func runSpeech(ctx context.Context, model, voice, text string, interactive bool) error {
	if text != "" || !interactive {
		if text == "" {
			return fmt.Errorf("speech models require input text. Usage: ollama run %s \"your text here\"", model)
		}
		return speak(ctx, model, voice, text)
	}
	if _, err := player(model, ""); err != nil {
		return err
	}

	scanner, err := readline.New(readline.Prompt{
		Prompt:      ">>> ",
		Placeholder: "Enter text to speak (/bye to exit)",
	})
	if err != nil {
		return err
	}
	if envconfig.NoHistory() {
		scanner.HistoryDisable()
	}
	for {
		// Clear anything typed while audio played; readline redraws it.
		fmt.Print(readline.CursorBOL + readline.ClearToEOL)
		line, err := scanner.Readline()
		switch {
		case errors.Is(err, io.EOF):
			fmt.Println()
			return nil
		case errors.Is(err, readline.ErrInterrupt):
			if line == "" {
				fmt.Println("\nUse Ctrl + d or /bye to exit.")
			}
			continue
		case errors.Is(err, readline.ErrEditPrompt):
			continue
		case err != nil:
			return err
		}
		text := strings.TrimSpace(line)
		if text == "/bye" {
			return nil
		}
		if text == "" {
			continue
		}
		// Ctrl+C stops speaking and returns to the prompt.
		speakCtx, stop := signal.NotifyContext(ctx, os.Interrupt)
		if err := speak(speakCtx, model, voice, text); err != nil && speakCtx.Err() == nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
		}
		stop()
	}
}

// speak plays the audio, or writes it as WAV when stdout is not a terminal,
// for example `ollama run model "text" > speech.wav` on a machine without
// speakers.
func speak(ctx context.Context, model, voice, text string) error {
	toTerminal := term.IsTerminal(int(os.Stdout.Fd()))
	if toTerminal {
		if _, err := player(model, ""); err != nil {
			return err
		}
	}
	wav, err := synthesize(ctx, model, voice, text)
	if err != nil {
		return err
	}
	if !toTerminal {
		_, err := os.Stdout.Write(wav)
		return err
	}
	return play(ctx, model, wav)
}

func synthesize(ctx context.Context, model, voice, text string) ([]byte, error) {
	body, err := json.Marshal(map[string]string{"model": model, "voice": voice, "input": text, "response_format": "wav"})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, envconfig.Host().JoinPath("v1/audio/speech").String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	data, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		var e struct{ Error struct{ Message string } }
		if json.Unmarshal(data, &e) == nil && e.Error.Message != "" {
			return nil, errors.New(e.Error.Message)
		}
		return nil, fmt.Errorf("speech request failed: %s", res.Status)
	}
	if len(data) <= 44 {
		return nil, errors.New("speech response contained no audio")
	}
	// The server streams WAV of unknown length; record the length for players.
	binary.LittleEndian.PutUint32(data[4:], uint32(len(data)-8))
	binary.LittleEndian.PutUint32(data[40:], uint32(len(data)-44))
	return data, nil
}

// play uses the system's built-in WAV player.
func play(ctx context.Context, model string, wav []byte) error {
	f, err := os.CreateTemp("", "ollama-*.wav")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(wav); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}

	p, err := player(model, f.Name())
	if err != nil {
		return err
	}
	c := exec.CommandContext(ctx, p[0], p[1:]...)
	c.Stderr = os.Stderr
	if err := c.Run(); err != nil {
		return fmt.Errorf("%s failed: %w. %s", p[0], err, saveHint(model))
	}
	return nil
}

// player returns the command that plays the WAV file at path.
func player(model, path string) ([]string, error) {
	players := [][]string{{"paplay", path}, {"pw-play", path}, {"aplay", "-q", path}, {"ffplay", "-nodisp", "-autoexit", "-loglevel", "error", path}}
	switch runtime.GOOS {
	case "darwin":
		players = [][]string{{"afplay", path}}
	case "windows":
		players = [][]string{{"powershell", "-NoProfile", "-Command", "(New-Object Media.SoundPlayer '" + strings.ReplaceAll(path, "'", "''") + "').PlaySync()"}}
	}
	for _, p := range players {
		if _, err := exec.LookPath(p[0]); err == nil {
			return p, nil
		}
	}
	return nil, errors.New("no audio player found. " + saveHint(model))
}

func saveHint(model string) string {
	return fmt.Sprintf(`To save the audio instead, run: ollama run %s "text" > speech.wav`, model)
}
