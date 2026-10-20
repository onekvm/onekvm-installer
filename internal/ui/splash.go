package ui

import (
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

const (
	splashDuration = 3 * time.Second
	splashInterval = time.Second / 60
)

type splashTickMsg struct{ at time.Time }

type splash struct {
	started time.Time
	elapsed time.Duration
	raster  *splashRaster
}

func (s splash) tick() tea.Cmd {
	delay := min(splashInterval, max(0, time.Until(s.started.Add(splashDuration))))
	return tea.Tick(delay, func(now time.Time) tea.Msg { return splashTickMsg{now} })
}

type splashPoint struct{ x, y float64 }
type splashRGB struct{ r, g, b uint8 }
type splashPixel struct {
	color uint32
	set   bool
}

type splashParticle struct {
	dx, dy, jitterX, jitterY, noise, wash float64
	blue                                  bool
}

type splashRaster struct {
	width, height int
	pixels        []splashPixel
	particles     []splashParticle
}

// Exact 1K monogram paths from OneKVM's official app icon, in its original
// 512-unit coordinates. Native half-block rendering works over plain SSH;
// no image protocol, rasterizer, external process or runtime asset is needed.
var splashOne = [...]splashPoint{{78, 176}, {176, 78}, {238, 78}, {238, 444}, {150, 444}, {150, 204}, {78, 204}}
var splashK = [...]splashPoint{{238, 256}, {356, 78}, {454, 78}, {336, 256}, {458, 444}, {354, 444}}

func splashContains(p splashPoint, polygon []splashPoint) bool {
	inside := false
	j := len(polygon) - 1
	for i, a := range polygon {
		b := polygon[j]
		if (a.y > p.y) != (b.y > p.y) && p.x < (b.x-a.x)*(p.y-a.y)/(b.y-a.y)+a.x {
			inside = !inside
		}
		j = i
	}
	return inside
}

func splashEase(v float64) float64 {
	v = max(0, min(1, v))
	return v * v * (3 - 2*v)
}

func splashMix(a, b splashRGB, amount float64) splashRGB {
	channel := func(x, y uint8) uint8 { return uint8(math.Round(float64(x)*(1-amount) + float64(y)*amount)) }
	return splashRGB{channel(a.r, b.r), channel(a.g, b.g), channel(a.b, b.b)}
}

func (s *splash) view(width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	if s.raster == nil || s.raster.width != width || s.raster.height != height {
		s.raster = newSplashRaster(width, height)
	}
	raster := s.raster
	clear(raster.pixels)
	profile := lipgloss.ColorProfile()
	seconds := max(0, s.elapsed.Seconds())
	scatter := max(0, min(1, (seconds-1.85)/1.15))
	reveal := 0.18 + 0.82*splashEase(seconds/0.4)
	background, neutral, one := splashRGB{}, splashRGB{135, 145, 158}, splashRGB{255, 255, 255}
	// Do not query the terminal from View: OSC background detection can wait
	// five seconds and compete with Bubble Tea for keyboard input over SSH.
	terminalColors := strings.Split(os.Getenv("COLORFGBG"), ";")
	terminalBackground := terminalColors[len(terminalColors)-1]
	if terminalBackground == "7" || terminalBackground == "15" {
		background, one = splashRGB{255, 255, 255}, splashRGB{6, 26, 58}
	}
	blue := splashRGB{22, 119, 255} // #1677FF, the same brand blue as the wizard.
	centerX, centerY := float64(width-1)/2, float64(height*2-1)/2
	alpha := reveal * (1 - splashEase((scatter-0.2)/0.8))
	vanish := splashEase((scatter - 0.1) / 0.9)
	for _, p := range raster.particles {
		if scatter > 0 && p.noise < vanish {
			continue
		}
		spread := splashEase(max(0, (scatter-p.noise*0.12)/0.88))
		px := int(math.Round(centerX + p.dx*(1+spread*1.8) + p.jitterX*spread))
		py := int(math.Round(centerY + p.dy*(1+spread*1.8) + p.jitterY*spread))
		if px < 0 || px >= width || py < 0 || py >= height*2 {
			continue
		}
		target := one
		if p.blue {
			target = blue
		}
		saturation := splashEase((seconds - 0.3 - p.wash*0.35) / 0.9)
		color := splashMix(background, splashMix(neutral, target, saturation), alpha)
		raster.pixels[py*width+px] = splashPixel{color.terminal(profile), true}
	}
	return splashRender(raster.pixels, width, height, profile)
}

func newSplashRaster(width, height int) *splashRaster {
	r := &splashRaster{width: width, height: height, pixels: make([]splashPixel, width*height*2)}
	// Rasterize the fixed logo once per terminal size, rather than once per
	// frame. Two vertical pixels per row retain the original icon proportions.
	size := max(1, min(36, width-4, 2*(height-2)))
	rows := max(1, int(math.Round(float64(size)*366/380)))
	for y := 0; y < rows; y++ {
		for x := 0; x < size; x++ {
			point := splashPoint{78 + (float64(x)+0.5)*380/float64(size), 78 + (float64(y)+0.5)*366/float64(rows)}
			blue := splashContains(point, splashK[:])
			if !blue && !splashContains(point, splashOne[:]) {
				continue
			}
			// Stable per-pixel variation makes a cloud rather than an expanding
			// copy of the logo, and avoids flicker when the terminal is resized.
			seed := uint32(x+1)*374761393 ^ uint32(y+1)*668265263
			seed = (seed ^ (seed >> 13)) * 1274126177
			seed ^= seed >> 16
			r.particles = append(r.particles, splashParticle{
				dx: float64(x) - float64(size-1)/2, dy: float64(y) - float64(rows-1)/2,
				jitterX: (float64((seed>>16)&255)/255 - 0.5) * 18,
				jitterY: (float64((seed>>24)&255)/255 - 0.5) * 18,
				noise:   float64(seed&0xffff) / 65535,
				wash:    (float64(x)/float64(size) + float64(y)/float64(rows)) / 2,
				blue:    blue,
			})
		}
	}
	return r
}

// Integer palette mapping avoids the per-pixel hex parsing and perceptual
// color-space conversions in general-purpose styling on small RISC-V CPUs.
func (c splashRGB) terminal(profile termenv.Profile) uint32 {
	if profile == termenv.TrueColor {
		return uint32(c.r)<<16 | uint32(c.g)<<8 | uint32(c.b)
	}
	if profile == termenv.Ascii {
		return 0
	}
	distance := func(r, g, b int) int {
		dr, dg, db := int(c.r)-r, int(c.g)-g, int(c.b)-b
		return dr*dr + dg*dg + db*db
	}
	if profile == termenv.ANSI {
		palette := [...]splashRGB{
			{0, 0, 0}, {128, 0, 0}, {0, 128, 0}, {128, 128, 0},
			{0, 0, 128}, {128, 0, 128}, {0, 128, 128}, {192, 192, 192},
			{128, 128, 128}, {255, 0, 0}, {0, 255, 0}, {255, 255, 0},
			{0, 0, 255}, {255, 0, 255}, {0, 255, 255}, {255, 255, 255},
		}
		best, nearest := 0, math.MaxInt
		for i, p := range palette {
			if d := distance(int(p.r), int(p.g), int(p.b)); d < nearest {
				best, nearest = i, d
			}
		}
		return uint32(best)
	}
	index := func(v uint8) int {
		if v < 48 {
			return 0
		}
		if v < 115 {
			return 1
		}
		return (int(v) - 35) / 40
	}
	r, g, b := index(c.r), index(c.g), index(c.b)
	levels := [...]int{0, 95, 135, 175, 215, 255}
	gray := max(0, min(23, ((int(c.r)+int(c.g)+int(c.b))/3-8+5)/10))
	value := 8 + 10*gray
	if distance(value, value, value) < distance(levels[r], levels[g], levels[b]) {
		return uint32(232 + gray)
	}
	return uint32(16 + 36*r + 6*g + b)
}

func splashWriteColor(frame *strings.Builder, color uint32, background bool, profile termenv.Profile) {
	var scratch [32]byte
	seq := append(scratch[:0], "\x1b["...)
	switch profile {
	case termenv.TrueColor, termenv.ANSI256:
		if background {
			seq = append(seq, "48;"...)
		} else {
			seq = append(seq, "38;"...)
		}
		if profile == termenv.TrueColor {
			seq = append(seq, "2;"...)
			seq = strconv.AppendUint(seq, uint64(color>>16), 10)
			seq = append(seq, ';')
			seq = strconv.AppendUint(seq, uint64((color>>8)&255), 10)
			seq = append(seq, ';')
			seq = strconv.AppendUint(seq, uint64(color&255), 10)
		} else {
			seq = append(seq, "5;"...)
			seq = strconv.AppendUint(seq, uint64(color), 10)
		}
	case termenv.ANSI:
		code := 30 + color
		if color >= 8 {
			code = 90 + color - 8
		}
		if background {
			code += 10
		}
		seq = strconv.AppendUint(seq, uint64(code), 10)
	default:
		return
	}
	seq = append(seq, 'm')
	frame.Write(seq)
}

func splashRender(pixels []splashPixel, width, height int, profile termenv.Profile) string {
	var frame strings.Builder
	frame.Grow(width * height * 6)
	for y := 0; y < height; y++ {
		if y > 0 {
			frame.WriteByte('\n')
		}
		// Leave trailing cells empty to avoid wrapping at the terminal edge.
		end := width
		for end > 0 && !pixels[y*2*width+end-1].set && !pixels[(y*2+1)*width+end-1].set {
			end--
		}
		var foreground, background splashPixel
		for x := 0; x < end; {
			top, bottom := pixels[y*2*width+x], pixels[(y*2+1)*width+x]
			run := 1
			for x+run < end && pixels[y*2*width+x+run] == top && pixels[(y*2+1)*width+x+run] == bottom {
				run++
			}
			glyph := " "
			var fg, bg splashPixel
			switch {
			case top.set && bottom.set && top.color == bottom.color:
				fg, glyph = top, "█"
			case top.set && bottom.set:
				fg, bg, glyph = top, bottom, "▀"
			case top.set:
				fg, glyph = top, "▀"
			case bottom.set:
				fg, glyph = bottom, "▄"
			}
			if profile != termenv.Ascii {
				if fg.set && fg != foreground {
					splashWriteColor(&frame, fg.color, false, profile)
					foreground = fg
				}
				if bg != background {
					if bg.set {
						splashWriteColor(&frame, bg.color, true, profile)
					} else {
						frame.WriteString("\x1b[49m")
					}
					background = bg
				}
			}
			for i := 0; i < run; i++ {
				frame.WriteString(glyph)
			}
			x += run
		}
		// Bubble Tea can skip unchanged lines. Make every line self-contained
		// so a skipped row cannot leave colors active for the next row or menu.
		if foreground.set || background.set {
			frame.WriteString("\x1b[0m")
		}
	}
	return frame.String()
}
