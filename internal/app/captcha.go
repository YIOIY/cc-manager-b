package app

import (
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	captchaTTL      = 5 * time.Minute
	captchaMaxItems = 10000
	captchaLength   = 4
	captchaCharset  = "23456789ABCDEFGHJKLMNPQRSTUVWXYZ"
)

type captchaEntry struct {
	answer  string
	expires time.Time
}

// captchaStore holds one-time login challenges in memory.
type captchaStore struct {
	mu    sync.Mutex
	items map[string]captchaEntry
}

func newCaptchaStore() *captchaStore {
	return &captchaStore{items: map[string]captchaEntry{}}
}

func (s *captchaStore) put(id, answer string, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.items) >= captchaMaxItems {
		s.sweepLocked(now)
		if len(s.items) >= captchaMaxItems {
			// Drop an arbitrary expired-or-oldest entry so issuance cannot stall.
			var drop string
			var when time.Time
			for k, v := range s.items {
				if drop == "" || v.expires.Before(when) {
					drop, when = k, v.expires
				}
			}
			if drop != "" {
				delete(s.items, drop)
			}
		}
	}
	s.items[id] = captchaEntry{answer: answer, expires: now.Add(captchaTTL)}
}

// consume returns whether input matches the challenge, then removes it.
func (s *captchaStore) consume(id, input string, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.items[id]
	if !ok {
		return false
	}
	delete(s.items, id)
	if now.After(e.expires) {
		return false
	}
	want := []byte(strings.ToUpper(strings.TrimSpace(e.answer)))
	got := []byte(strings.ToUpper(strings.TrimSpace(input)))
	return subtle.ConstantTimeCompare(want, got) == 1
}

// peek returns the answer without consuming it. Tests only.
func (s *captchaStore) peek(id string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.items[id]
	if !ok || time.Now().After(e.expires) {
		return "", false
	}
	return e.answer, true
}

func (s *captchaStore) sweep(now time.Time) {
	s.mu.Lock()
	s.sweepLocked(now)
	s.mu.Unlock()
}

func (s *captchaStore) sweepLocked(now time.Time) {
	for id, e := range s.items {
		if now.After(e.expires) {
			delete(s.items, id)
		}
	}
}

func randomCaptchaAnswer() string {
	b := make([]byte, captchaLength)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	out := make([]byte, captchaLength)
	for i := range b {
		out[i] = captchaCharset[int(b[i])%len(captchaCharset)]
	}
	return string(out)
}

// authCaptcha issues a fresh graphical challenge for the login form.
func (a *App) authCaptcha(w http.ResponseWriter, r *http.Request) {
	if !a.sameOrigin(r) {
		fail(w, http.StatusForbidden, "ORIGIN_FAILED", "请求来源不匹配")
		return
	}
	// Ensure the browser has a session cookie so login CSRF still works.
	if _, _, ok := a.currentSession(r); !ok {
		a.issueSession(w, r, "")
	}
	id := randomString()
	answer := randomCaptchaAnswer()
	img, err := renderCaptchaPNG(answer)
	if err != nil {
		serverError(w, r, "INTERNAL_ERROR", "无法生成验证码", err)
		return
	}
	a.captchas.put(id, answer, time.Now())
	body := map[string]any{
		"captcha_id": id,
		"image":      "data:image/png;base64," + base64.StdEncoding.EncodeToString(img),
	}
	if a.cfg.CaptchaDebug {
		body["answer"] = answer
	}
	write(w, http.StatusOK, body)
}

// verifyLoginCaptcha consumes captcha_id. Missing or wrong answers fail closed.
func (a *App) verifyLoginCaptcha(id, input string) bool {
	id = strings.TrimSpace(id)
	if id == "" || strings.TrimSpace(input) == "" {
		return false
	}
	return a.captchas.consume(id, input, time.Now())
}

func renderCaptchaPNG(text string) ([]byte, error) {
	const (
		width  = 140
		height = 44
		scale  = 3
	)
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	bg := color.RGBA{R: 245, G: 246, B: 248, A: 255}
	fg := color.RGBA{R: 28, G: 31, B: 35, A: 255}
	noise := color.RGBA{R: 180, G: 185, B: 190, A: 255}
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, bg)
		}
	}
	// Light noise dots.
	for i := 0; i < 80; i++ {
		x, y := randInt(width), randInt(height)
		img.Set(x, y, noise)
	}
	// Interference lines.
	for i := 0; i < 3; i++ {
		x0, y0 := randInt(width), randInt(height)
		x1, y1 := randInt(width), randInt(height)
		drawLine(img, x0, y0, x1, y1, noise)
	}
	startX := 12
	for i, ch := range text {
		glyph, ok := captchaGlyphs[ch]
		if !ok {
			continue
		}
		ox := startX + i*(5*scale+8)
		oy := 8 + randInt(5)
		drawGlyph(img, glyph, ox, oy, scale, fg)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func randInt(n int) int {
	if n <= 0 {
		return 0
	}
	var b [1]byte
	_, _ = rand.Read(b[:])
	return int(b[0]) % n
}

func drawLine(img *image.RGBA, x0, y0, x1, y1 int, c color.Color) {
	dx := abs(x1 - x0)
	dy := -abs(y1 - y0)
	sx, sy := 1, 1
	if x0 > x1 {
		sx = -1
	}
	if y0 > y1 {
		sy = -1
	}
	err := dx + dy
	for {
		img.Set(x0, y0, c)
		if x0 == x1 && y0 == y1 {
			break
		}
		e2 := 2 * err
		if e2 >= dy {
			err += dy
			x0 += sx
		}
		if e2 <= dx {
			err += dx
			y0 += sy
		}
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func drawGlyph(img *image.RGBA, rows [7]uint8, ox, oy, scale int, c color.Color) {
	for row := 0; row < 7; row++ {
		bits := rows[row]
		for col := 0; col < 5; col++ {
			if bits&(1<<uint(4-col)) == 0 {
				continue
			}
			for dy := 0; dy < scale; dy++ {
				for dx := 0; dx < scale; dx++ {
					img.Set(ox+col*scale+dx, oy+row*scale+dy, c)
				}
			}
		}
	}
}

// captchaGlyphs is a compact 5×7 bitmap font for the captcha charset.
var captchaGlyphs = map[rune][7]uint8{
	'2': {0x1F, 0x01, 0x01, 0x1F, 0x10, 0x10, 0x1F},
	'3': {0x1F, 0x01, 0x01, 0x0F, 0x01, 0x01, 0x1F},
	'4': {0x11, 0x11, 0x11, 0x1F, 0x01, 0x01, 0x01},
	'5': {0x1F, 0x10, 0x10, 0x1F, 0x01, 0x01, 0x1F},
	'6': {0x1F, 0x10, 0x10, 0x1F, 0x11, 0x11, 0x1F},
	'7': {0x1F, 0x01, 0x01, 0x02, 0x04, 0x04, 0x04},
	'8': {0x1F, 0x11, 0x11, 0x1F, 0x11, 0x11, 0x1F},
	'9': {0x1F, 0x11, 0x11, 0x1F, 0x01, 0x01, 0x1F},
	'A': {0x0E, 0x11, 0x11, 0x1F, 0x11, 0x11, 0x11},
	'B': {0x1E, 0x11, 0x11, 0x1E, 0x11, 0x11, 0x1E},
	'C': {0x0F, 0x10, 0x10, 0x10, 0x10, 0x10, 0x0F},
	'D': {0x1E, 0x11, 0x11, 0x11, 0x11, 0x11, 0x1E},
	'E': {0x1F, 0x10, 0x10, 0x1E, 0x10, 0x10, 0x1F},
	'F': {0x1F, 0x10, 0x10, 0x1E, 0x10, 0x10, 0x10},
	'G': {0x0F, 0x10, 0x10, 0x13, 0x11, 0x11, 0x0F},
	'H': {0x11, 0x11, 0x11, 0x1F, 0x11, 0x11, 0x11},
	'J': {0x01, 0x01, 0x01, 0x01, 0x11, 0x11, 0x0E},
	'K': {0x11, 0x12, 0x14, 0x18, 0x14, 0x12, 0x11},
	'L': {0x10, 0x10, 0x10, 0x10, 0x10, 0x10, 0x1F},
	'M': {0x11, 0x1B, 0x15, 0x11, 0x11, 0x11, 0x11},
	'N': {0x11, 0x19, 0x15, 0x13, 0x11, 0x11, 0x11},
	'P': {0x1E, 0x11, 0x11, 0x1E, 0x10, 0x10, 0x10},
	'Q': {0x0E, 0x11, 0x11, 0x11, 0x15, 0x12, 0x0D},
	'R': {0x1E, 0x11, 0x11, 0x1E, 0x14, 0x12, 0x11},
	'S': {0x0F, 0x10, 0x10, 0x0E, 0x01, 0x01, 0x1E},
	'T': {0x1F, 0x04, 0x04, 0x04, 0x04, 0x04, 0x04},
	'U': {0x11, 0x11, 0x11, 0x11, 0x11, 0x11, 0x0E},
	'V': {0x11, 0x11, 0x11, 0x11, 0x11, 0x0A, 0x04},
	'W': {0x11, 0x11, 0x11, 0x15, 0x15, 0x1B, 0x11},
	'X': {0x11, 0x11, 0x0A, 0x04, 0x0A, 0x11, 0x11},
	'Y': {0x11, 0x11, 0x0A, 0x04, 0x04, 0x04, 0x04},
	'Z': {0x1F, 0x01, 0x02, 0x04, 0x08, 0x10, 0x1F},
}
