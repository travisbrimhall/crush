// Package anim provides an animated spinner.
package anim

import (
	"fmt"
	"image/color"
	"math"
	"math/rand/v2"
	"strings"
	"sync/atomic"
	"time"

	"github.com/zeebo/xxh3"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/lucasb-eyer/go-colorful"

	"github.com/charmbracelet/crush/internal/csync"
)

const (
	fps           = 20
	initialChar   = '.'
	labelGap      = " "
	labelGapWidth = 1

	// Periods of ellipsis animation speed in steps.
	//
	// If the FPS is 20 (50 milliseconds) this means that the ellipsis will
	// change every 8 frames (400 milliseconds).
	ellipsisAnimSpeed = 8

	// Number of frames to prerender for the animation. After this number
	// of frames, the animation will loop. This only applies when color
	// cycling is disabled.
	prerenderedFrames = 10

	// Default number of cycling chars.
	defaultNumCyclingChars = 10
)

// Default colors for gradient.
var (
	defaultGradColorA = color.RGBA{R: 0xff, G: 0, B: 0, A: 0xff}
	defaultGradColorB = color.RGBA{R: 0, G: 0, B: 0xff, A: 0xff}
	defaultLabelColor = color.RGBA{R: 0xcc, G: 0xcc, B: 0xcc, A: 0xff}
)

// Style defines the animation style.
type Style int

const (
	// StyleMatrix is the default scrambled character animation.
	StyleMatrix Style = iota
	// StylePulse uses fading block characters.
	StylePulse
	// StyleSineWave uses vertical bar characters in a wave pattern.
	StyleSineWave
)

// ParseStyle converts a string to a Style. Returns StyleMatrix for unknown values.
func ParseStyle(s string) Style {
	switch s {
	case "pulse":
		return StylePulse
	case "sinewave":
		return StyleSineWave
	default:
		return StyleMatrix
	}
}

var (
	availableRunes = []rune("0123456789abcdefABCDEF~!@#$£€%^&*()+=_")
	ellipsisFrames = []string{".", "..", "...", ""}
	// Block characters for pulse animation (full to empty).
	pulseChars = []rune{'█', '▓', '▒', '░', ' '}
	// Vertical bar characters for sine wave animation (low to high).
	sineWaveChars = []rune{'▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}
)

// Internal ID management. Used during animating to ensure that frame messages
// are received only by spinner components that sent them.
var lastID int64

func nextID() int {
	return int(atomic.AddInt64(&lastID, 1))
}

// Cache for expensive animation calculations
type animCache struct {
	initialFrames  [][]string
	cyclingFrames  [][]string
	width          int
	labelWidth     int
	label          []string
	ellipsisFrames []string
}

var animCacheMap = csync.NewMap[string, *animCache]()

// settingsHash creates a hash key for the settings to use for caching.
func settingsHash(opts Settings) string {
	h := xxh3.New()
	fmt.Fprintf(h, "%d-%s-%v-%v-%v-%t-%d",
		opts.Size, opts.Label, opts.LabelColor, opts.GradColorA, opts.GradColorB, opts.CycleColors, opts.Style)
	return fmt.Sprintf("%x", h.Sum(nil))
}

// StepMsg is a message type used to trigger the next step in the animation.
type StepMsg struct{ ID string }

// Settings defines settings for the animation.
type Settings struct {
	ID          string
	Size        int
	Label       string
	LabelColor  color.Color
	GradColorA  color.Color
	GradColorB  color.Color
	CycleColors bool
	Style       Style
}

// Default settings.
const ()

// Anim is a Bubble for an animated spinner.
type Anim struct {
	width            int
	cyclingCharWidth int
	label            *csync.Slice[string]
	labelWidth       int
	labelColor       color.Color
	style            Style
	initialFrames    [][]string // frames for the initial characters
	cyclingFrames    [][]string           // frames for the cycling characters
	step             atomic.Int64         // current main frame step
	ellipsisStep     atomic.Int64         // current ellipsis frame step
	ellipsisFrames   *csync.Slice[string] // ellipsis animation frames
	id               string
}

// New creates a new Anim instance with the specified width and label.
func New(opts Settings) *Anim {
	a := &Anim{}
	// Validate settings.
	if opts.Size < 1 {
		opts.Size = defaultNumCyclingChars
	}
	if colorIsUnset(opts.GradColorA) {
		opts.GradColorA = defaultGradColorA
	}
	if colorIsUnset(opts.GradColorB) {
		opts.GradColorB = defaultGradColorB
	}
	if colorIsUnset(opts.LabelColor) {
		opts.LabelColor = defaultLabelColor
	}

	if opts.ID != "" {
		a.id = opts.ID
	} else {
		a.id = fmt.Sprintf("%d", nextID())
	}
	a.cyclingCharWidth = opts.Size
	a.labelColor = opts.LabelColor
	a.style = opts.Style

	// Check cache first
	cacheKey := settingsHash(opts)
	cached, exists := animCacheMap.Get(cacheKey)

	if exists {
		// Use cached values
		a.width = cached.width
		a.labelWidth = cached.labelWidth
		a.label = csync.NewSliceFrom(cached.label)
		a.ellipsisFrames = csync.NewSliceFrom(cached.ellipsisFrames)
		a.initialFrames = cached.initialFrames
		a.cyclingFrames = cached.cyclingFrames
	} else {
		// Generate new values and cache them
		a.labelWidth = lipgloss.Width(opts.Label)

		// Total width of anim, in cells.
		a.width = opts.Size
		if opts.Label != "" {
			a.width += labelGapWidth + lipgloss.Width(opts.Label)
		}

		// Render the label
		a.renderLabel(opts.Label)

		// Pre-generate gradient.
		var ramp []color.Color
		numFrames := prerenderedFrames
		if opts.CycleColors {
			ramp = makeGradientRamp(a.width*3, opts.GradColorA, opts.GradColorB, opts.GradColorA, opts.GradColorB)
			numFrames = a.width * 2
		} else {
			ramp = makeGradientRamp(a.width, opts.GradColorA, opts.GradColorB)
		}

		// Pre-render initial characters.
		a.initialFrames = make([][]string, numFrames)
		offset := 0
		for i := range a.initialFrames {
			a.initialFrames[i] = make([]string, a.width+labelGapWidth+a.labelWidth)
			for j := range a.initialFrames[i] {
				if j+offset >= len(ramp) {
					continue // skip if we run out of colors
				}

				var c color.Color
				if j <= a.cyclingCharWidth {
					c = ramp[j+offset]
				} else {
					c = opts.LabelColor
				}

				// Also prerender the initial character with Lip Gloss to avoid
				// processing in the render loop.
				a.initialFrames[i][j] = lipgloss.NewStyle().
					Foreground(c).
					Render(string(initialChar))
			}
			if opts.CycleColors {
				offset++
			}
		}

		// Prerender frames for the animation based on style.
		a.cyclingFrames = make([][]string, numFrames)
		offset = 0
		for i := range a.cyclingFrames {
			a.cyclingFrames[i] = make([]string, a.width)
			for j := range a.cyclingFrames[i] {
				if j+offset >= len(ramp) {
					continue // skip if we run out of colors
				}

				var r rune
				switch opts.Style {
				case StylePulse:
					// Pulse animation: cycle through block characters based on frame.
					pulseIdx := (i + j) % len(pulseChars)
					r = pulseChars[pulseIdx]
				case StyleSineWave:
					// Sine wave animation: use position and frame to create wave.
					phase := float64(i)/float64(numFrames)*2*math.Pi + float64(j)*0.5
					sineVal := (math.Sin(phase) + 1) / 2 // Normalize to 0-1.
					charIdx := int(sineVal * float64(len(sineWaveChars)-1))
					r = sineWaveChars[charIdx]
				default:
					// StyleMatrix: scrambled random characters.
					r = availableRunes[rand.IntN(len(availableRunes))]
				}

				a.cyclingFrames[i][j] = lipgloss.NewStyle().
					Foreground(ramp[j+offset]).
					Render(string(r))
			}
			if opts.CycleColors {
				offset++
			}
		}

		// Cache the results
		labelSlice := make([]string, a.label.Len())
		for i, v := range a.label.Seq2() {
			labelSlice[i] = v
		}
		ellipsisSlice := make([]string, a.ellipsisFrames.Len())
		for i, v := range a.ellipsisFrames.Seq2() {
			ellipsisSlice[i] = v
		}
		cached = &animCache{
			initialFrames:  a.initialFrames,
			cyclingFrames:  a.cyclingFrames,
			width:          a.width,
			labelWidth:     a.labelWidth,
			label:          labelSlice,
			ellipsisFrames: ellipsisSlice,
		}
		animCacheMap.Set(cacheKey, cached)
	}

	return a
}

// SetLabel updates the label text and re-renders it.
func (a *Anim) SetLabel(newLabel string) {
	a.labelWidth = lipgloss.Width(newLabel)

	// Update total width
	a.width = a.cyclingCharWidth
	if newLabel != "" {
		a.width += labelGapWidth + a.labelWidth
	}

	// Re-render the label
	a.renderLabel(newLabel)
}

// renderLabel renders the label with the current label color.
func (a *Anim) renderLabel(label string) {
	if a.labelWidth > 0 {
		// Pre-render the label.
		labelRunes := []rune(label)
		a.label = csync.NewSlice[string]()
		for i := range labelRunes {
			rendered := lipgloss.NewStyle().
				Foreground(a.labelColor).
				Render(string(labelRunes[i]))
			a.label.Append(rendered)
		}

		// Pre-render the ellipsis frames which come after the label.
		a.ellipsisFrames = csync.NewSlice[string]()
		for _, frame := range ellipsisFrames {
			rendered := lipgloss.NewStyle().
				Foreground(a.labelColor).
				Render(frame)
			a.ellipsisFrames.Append(rendered)
		}
	} else {
		a.label = csync.NewSlice[string]()
		a.ellipsisFrames = csync.NewSlice[string]()
	}
}

// Width returns the total width of the animation.
func (a *Anim) Width() (w int) {
	w = a.width
	if a.labelWidth > 0 {
		w += labelGapWidth + a.labelWidth

		var widestEllipsisFrame int
		for _, f := range ellipsisFrames {
			fw := lipgloss.Width(f)
			if fw > widestEllipsisFrame {
				widestEllipsisFrame = fw
			}
		}
		w += widestEllipsisFrame
	}
	return w
}

// Start starts the animation.
func (a *Anim) Start() tea.Cmd {
	return a.Step()
}

// Animate advances the animation to the next step.
func (a *Anim) Animate(msg StepMsg) tea.Cmd {
	if msg.ID != a.id {
		return nil
	}

	step := a.step.Add(1)
	if int(step) >= len(a.cyclingFrames) {
		a.step.Store(0)
	}

	if a.labelWidth > 0 {
		// Manage the ellipsis animation.
		ellipsisStep := a.ellipsisStep.Add(1)
		if int(ellipsisStep) >= ellipsisAnimSpeed*len(ellipsisFrames) {
			a.ellipsisStep.Store(0)
		}
	}
	return a.Step()
}

// Render renders the current state of the animation.
func (a *Anim) Render() string {
	var b strings.Builder
	step := int(a.step.Load())
	for i := range a.width {
		switch {
		case i < a.cyclingCharWidth:
			// Render a cycling character.
			b.WriteString(a.cyclingFrames[step][i])
		case i == a.cyclingCharWidth:
			// Render label gap.
			b.WriteString(labelGap)
		case i > a.cyclingCharWidth:
			// Label.
			if labelChar, ok := a.label.Get(i - a.cyclingCharWidth - labelGapWidth); ok {
				b.WriteString(labelChar)
			}
		}
	}
	// Render animated ellipsis at the end of the label if all characters
	// have been initialized.
	if a.labelWidth > 0 {
		ellipsisStep := int(a.ellipsisStep.Load())
		if ellipsisFrame, ok := a.ellipsisFrames.Get(ellipsisStep / ellipsisAnimSpeed); ok {
			b.WriteString(ellipsisFrame)
		}
	}

	return b.String()
}

// Step is a command that triggers the next step in the animation.
func (a *Anim) Step() tea.Cmd {
	return tea.Tick(time.Second/time.Duration(fps), func(t time.Time) tea.Msg {
		return StepMsg{ID: a.id}
	})
}

// makeGradientRamp() returns a slice of colors blended between the given keys.
// Blending is done as Hcl to stay in gamut.
func makeGradientRamp(size int, stops ...color.Color) []color.Color {
	if len(stops) < 2 {
		return nil
	}

	points := make([]colorful.Color, len(stops))
	for i, k := range stops {
		points[i], _ = colorful.MakeColor(k)
	}

	numSegments := len(stops) - 1
	if numSegments == 0 {
		return nil
	}
	blended := make([]color.Color, 0, size)

	// Calculate how many colors each segment should have.
	segmentSizes := make([]int, numSegments)
	baseSize := size / numSegments
	remainder := size % numSegments

	// Distribute the remainder across segments.
	for i := range numSegments {
		segmentSizes[i] = baseSize
		if i < remainder {
			segmentSizes[i]++
		}
	}

	// Generate colors for each segment.
	for i := range numSegments {
		c1 := points[i]
		c2 := points[i+1]
		segmentSize := segmentSizes[i]

		for j := range segmentSize {
			if segmentSize == 0 {
				continue
			}
			t := float64(j) / float64(segmentSize)
			c := c1.BlendHcl(c2, t)
			blended = append(blended, c)
		}
	}

	return blended
}

func colorIsUnset(c color.Color) bool {
	if c == nil {
		return true
	}
	_, _, _, a := c.RGBA()
	return a == 0
}
