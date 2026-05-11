package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"
)

// Player streams 16-bit PCM directly to the system default speaker
// via a subprocess (aplay / paplay / ffplay) reading from stdin.
// No file is ever written.
type Player struct {
	cmdName string
	mu      sync.Mutex
}

func NewPlayer(cmdName string) *Player {
	return &Player{cmdName: cmdName}
}

// Play blocks until playback finishes.
// Serialized via mutex — one utterance at a time.
func (p *Player) Play(pcm []int16, sampleRate int) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	args := buildPlayerArgs(p.cmdName, sampleRate)
	cmd := exec.Command(args[0], args[1:]...)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe: %w", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("%s start: %w", args[0], err)
	}

	if err := binary.Write(stdin, binary.LittleEndian, pcm); err != nil {
		_ = stdin.Close()
		_ = cmd.Wait()
		return fmt.Errorf("write pcm: %w", err)
	}
	_ = stdin.Close()

	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("%s: %v: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func buildPlayerArgs(name string, sampleRate int) []string {
	sr := strconv.Itoa(sampleRate)
	switch name {
	case "aplay":
		// ALSA — works on bare Linux (Genio Yocto, Raspberry Pi OS without PulseAudio).
		return []string{"aplay", "-q", "-r", sr, "-f", "S16_LE", "-c", "1", "-"}
	case "paplay":
		// PulseAudio / PipeWire — works on most desktop distros.
		return []string{"paplay", "--rate=" + sr, "--format=s16le", "--channels=1", "--raw"}
	case "ffplay":
		// Universal fallback — handles whichever sink is available.
		return []string{"ffplay", "-nodisp", "-autoexit", "-loglevel", "quiet",
			"-f", "s16le", "-ar", sr, "-ac", "1", "-i", "pipe:0"}
	default:
		// Custom command (space-separated). PCM is fed via stdin.
		return strings.Fields(name)
	}
}
