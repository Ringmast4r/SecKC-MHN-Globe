package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"math/rand"
	"net"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/gdamore/tcell/v2"
)

// ============================================================================
// CORE DATA STRUCTURES
// ============================================================================

type Connection struct {
	IP       string
	Username string
	Password string
	Protocol string
	Time     time.Time
	City     string // City name
	Country  string // Country code
	ASN      string // Autonomous System Number
	Org      string // Organization/ISP
	RDNS     string // Reverse DNS
}

type APIConfig struct {
	BaseURL      string
	PollInterval time.Duration
	MaxEvents    int
}

type APIClient struct {
	config      *APIConfig
	httpClient  *http.Client
	lastEventTS float64
}

type APIEvent struct {
	Event     map[string]interface{} `json:"event"`
	Timestamp float64                `json:"timestamp"`
	CachedAt  string                 `json:"cached_at"`
}

type APIResponse struct {
	Events        []APIEvent `json:"events"`
	Count         int        `json:"count"`
	Authenticated bool       `json:"authenticated"`
	ServerTime    float64    `json:"server_time"`
}

type GeocodeResponse struct {
	City struct {
		Names map[string]string `json:"names"`
	} `json:"city"`
	Country struct {
		ISOCode string            `json:"iso_code"`
		Names   map[string]string `json:"names"`
	} `json:"country"`
	Location struct {
		Latitude  float64 `json:"latitude"`
		Longitude float64 `json:"longitude"`
	} `json:"location"`
}

type LocationInfo struct {
	City      string
	Country   string
	Latitude  float64
	Longitude float64
	ASN       string // Autonomous System Number
	Org       string // Organization/ISP name
	RDNS      string // Reverse DNS
	Valid     bool
}

type GeocodeCache struct {
	IP        string
	Location  LocationInfo
	Timestamp time.Time
}

type GeoIPManager struct {
	apiClient *APIClient
	cache     map[string]GeocodeCache
	cacheList []string
	maxCache  int
	mutex     sync.RWMutex
}

type Dashboard struct {
	Connections []Connection
	MaxLines    int
	mutex       sync.RWMutex
}

type HourlyStats struct {
	Date    string         `json:"date"`
	Hourly  map[string]int `json:"hourly"`
	Channel string         `json:"channel"`
}

type StatsResponse []HourlyStats

type StatsManager struct {
	todayData     StatsResponse
	yesterdayData StatsResponse
	lastFetch     time.Time
	mutex         sync.RWMutex
	todayURL      string
	yesterdayURL  string
}

// ============================================================================
// THEME SYSTEM
// ============================================================================

type Theme struct {
	Name            string
	Background      tcell.Color
	Text            tcell.Color
	Globe           tcell.Color
	GlobeShaded     tcell.Color
	Attack          tcell.Color
	AttackGlyph     tcell.Color
	Dashboard       tcell.Color
	Stats           tcell.Color
	Separator       tcell.Color
	StatusOk        tcell.Color
	StatusError     tcell.Color
	ArcTrail        tcell.Color
	RainEffect      tcell.Color
	ScanlineShade   float64 // 0.0-1.0 dimming factor for CRT scanlines
}

var themes = map[string]*Theme{
	"default": {
		Name:          "default",
		Background:    tcell.ColorBlack,
		Text:          tcell.ColorWhite,
		Globe:         tcell.ColorGreen,
		GlobeShaded:   tcell.NewRGBColor(0, 100, 0),
		Attack:        tcell.ColorRed,
		AttackGlyph:   tcell.NewRGBColor(255, 100, 100),
		Dashboard:     tcell.ColorYellow,
		Stats:         tcell.ColorAqua,
		Separator:     tcell.ColorGray,
		StatusOk:      tcell.ColorGreen,
		StatusError:   tcell.ColorRed,
		ArcTrail:      tcell.NewRGBColor(255, 150, 0),
		RainEffect:    tcell.ColorGreen,
		ScanlineShade: 0.7,
	},
	"matrix": {
		Name:          "matrix",
		Background:    tcell.ColorBlack,
		Text:          tcell.NewRGBColor(0, 255, 65),
		Globe:         tcell.NewRGBColor(0, 255, 65),
		GlobeShaded:   tcell.NewRGBColor(0, 150, 40),
		Attack:        tcell.NewRGBColor(0, 255, 100),
		AttackGlyph:   tcell.NewRGBColor(100, 255, 100),
		Dashboard:     tcell.NewRGBColor(0, 200, 50),
		Stats:         tcell.NewRGBColor(0, 180, 45),
		Separator:     tcell.NewRGBColor(0, 100, 25),
		StatusOk:      tcell.NewRGBColor(0, 255, 65),
		StatusError:   tcell.NewRGBColor(0, 150, 40),
		ArcTrail:      tcell.NewRGBColor(0, 255, 100),
		RainEffect:    tcell.NewRGBColor(0, 255, 65),
		ScanlineShade: 0.6,
	},
	"amber": {
		Name:          "amber",
		Background:    tcell.ColorBlack,
		Text:          tcell.NewRGBColor(255, 176, 0),
		Globe:         tcell.NewRGBColor(255, 176, 0),
		GlobeShaded:   tcell.NewRGBColor(180, 120, 0),
		Attack:        tcell.NewRGBColor(255, 200, 50),
		AttackGlyph:   tcell.NewRGBColor(255, 220, 100),
		Dashboard:     tcell.NewRGBColor(255, 160, 0),
		Stats:         tcell.NewRGBColor(220, 140, 0),
		Separator:     tcell.NewRGBColor(120, 80, 0),
		StatusOk:      tcell.NewRGBColor(255, 176, 0),
		StatusError:   tcell.NewRGBColor(180, 100, 0),
		ArcTrail:      tcell.NewRGBColor(255, 200, 80),
		RainEffect:    tcell.NewRGBColor(255, 176, 0),
		ScanlineShade: 0.65,
	},
	"solarized": {
		Name:          "solarized",
		Background:    tcell.NewRGBColor(0, 43, 54),
		Text:          tcell.NewRGBColor(131, 148, 150),
		Globe:         tcell.NewRGBColor(42, 161, 152),
		GlobeShaded:   tcell.NewRGBColor(30, 110, 105),
		Attack:        tcell.NewRGBColor(220, 50, 47),
		AttackGlyph:   tcell.NewRGBColor(255, 100, 97),
		Dashboard:     tcell.NewRGBColor(181, 137, 0),
		Stats:         tcell.NewRGBColor(38, 139, 210),
		Separator:     tcell.NewRGBColor(88, 110, 117),
		StatusOk:      tcell.NewRGBColor(133, 153, 0),
		StatusError:   tcell.NewRGBColor(220, 50, 47),
		ArcTrail:      tcell.NewRGBColor(203, 75, 22),
		RainEffect:    tcell.NewRGBColor(42, 161, 152),
		ScanlineShade: 0.75,
	},
	"nord": {
		Name:          "nord",
		Background:    tcell.NewRGBColor(46, 52, 64),
		Text:          tcell.NewRGBColor(216, 222, 233),
		Globe:         tcell.NewRGBColor(136, 192, 208),
		GlobeShaded:   tcell.NewRGBColor(94, 129, 172),
		Attack:        tcell.NewRGBColor(191, 97, 106),
		AttackGlyph:   tcell.NewRGBColor(235, 147, 156),
		Dashboard:     tcell.NewRGBColor(235, 203, 139),
		Stats:         tcell.NewRGBColor(129, 161, 193),
		Separator:     tcell.NewRGBColor(76, 86, 106),
		StatusOk:      tcell.NewRGBColor(163, 190, 140),
		StatusError:   tcell.NewRGBColor(191, 97, 106),
		ArcTrail:      tcell.NewRGBColor(208, 135, 112),
		RainEffect:    tcell.NewRGBColor(136, 192, 208),
		ScanlineShade: 0.8,
	},
	"dracula": {
		Name:          "dracula",
		Background:    tcell.NewRGBColor(40, 42, 54),
		Text:          tcell.NewRGBColor(248, 248, 242),
		Globe:         tcell.NewRGBColor(80, 250, 123),
		GlobeShaded:   tcell.NewRGBColor(50, 150, 80),
		Attack:        tcell.NewRGBColor(255, 85, 85),
		AttackGlyph:   tcell.NewRGBColor(255, 121, 198),
		Dashboard:     tcell.NewRGBColor(241, 250, 140),
		Stats:         tcell.NewRGBColor(139, 233, 253),
		Separator:     tcell.NewRGBColor(98, 114, 164),
		StatusOk:      tcell.NewRGBColor(80, 250, 123),
		StatusError:   tcell.NewRGBColor(255, 85, 85),
		ArcTrail:      tcell.NewRGBColor(255, 184, 108),
		RainEffect:    tcell.NewRGBColor(189, 147, 249),
		ScanlineShade: 0.7,
	},
	"mono": {
		Name:          "mono",
		Background:    tcell.ColorBlack,
		Text:          tcell.ColorWhite,
		Globe:         tcell.ColorWhite,
		GlobeShaded:   tcell.ColorGray,
		Attack:        tcell.ColorWhite,
		AttackGlyph:   tcell.ColorWhite,
		Dashboard:     tcell.ColorWhite,
		Stats:         tcell.ColorWhite,
		Separator:     tcell.ColorWhite,
		StatusOk:      tcell.ColorWhite,
		StatusError:   tcell.ColorWhite,
		ArcTrail:      tcell.ColorWhite,
		RainEffect:    tcell.ColorWhite,
		ScanlineShade: 0.5,
	},
	"rainbow": {
		Name:          "rainbow",
		Background:    tcell.ColorBlack,
		Text:          tcell.NewRGBColor(255, 255, 255),
		Globe:         tcell.NewRGBColor(255, 0, 0), // Base color (will be rainbow pattern)
		GlobeShaded:   tcell.NewRGBColor(128, 0, 0),
		Attack:        tcell.NewRGBColor(255, 255, 255),
		AttackGlyph:   tcell.NewRGBColor(255, 255, 100),
		Dashboard:     tcell.NewRGBColor(138, 43, 226),
		Stats:         tcell.NewRGBColor(0, 191, 255),
		Separator:     tcell.NewRGBColor(128, 128, 128),
		StatusOk:      tcell.NewRGBColor(0, 255, 0),
		StatusError:   tcell.NewRGBColor(255, 0, 0),
		ArcTrail:      tcell.NewRGBColor(255, 165, 0),
		RainEffect:    tcell.NewRGBColor(0, 255, 255),
		ScanlineShade: 0.7,
	},
	"skittles": {
		Name:          "skittles",
		Background:    tcell.ColorBlack,
		Text:          tcell.NewRGBColor(255, 255, 255),
		Globe:         tcell.NewRGBColor(255, 0, 0), // Base color (will be randomized per character)
		GlobeShaded:   tcell.NewRGBColor(128, 0, 0),
		Attack:        tcell.NewRGBColor(255, 255, 0),
		AttackGlyph:   tcell.NewRGBColor(255, 200, 0),
		Dashboard:     tcell.NewRGBColor(138, 43, 226),
		Stats:         tcell.NewRGBColor(0, 191, 255),
		Separator:     tcell.NewRGBColor(128, 128, 128),
		StatusOk:      tcell.NewRGBColor(0, 255, 0),
		StatusError:   tcell.NewRGBColor(255, 0, 0),
		ArcTrail:      tcell.NewRGBColor(255, 165, 0),
		RainEffect:    tcell.NewRGBColor(0, 255, 255),
		ScanlineShade: 0.7,
	},
}

var currentTheme *Theme

// ============================================================================
// CHARSET RENDERING (Braille, Blocks, ASCII)
// ============================================================================

type Charset int

const (
	CharsetASCII Charset = iota
	CharsetBlocks
	CharsetBraille
)

func densityToChar(density float64, charset Charset) rune {
	switch charset {
	case CharsetBraille:
		return densityToBraille(density)
	case CharsetBlocks:
		return densityToBlock(density)
	default: // CharsetASCII
		return densityToASCII(density)
	}
}

func densityToBraille(density float64) rune {
	// Unicode Braille patterns: U+2800 to U+28FF (256 patterns)
	// Map density 0.0-1.0 to braille dot patterns for visual density
	if density > 1.0 {
		return '⣿' // Full 8-dot pattern
	} else if density > 0.9 {
		return '⣾'
	} else if density > 0.8 {
		return '⣶'
	} else if density > 0.7 {
		return '⣦'
	} else if density > 0.6 {
		return '⣤'
	} else if density > 0.5 {
		return '⣀'
	} else if density > 0.4 {
		return '⡀'
	} else if density > 0.3 {
		return '⠄'
	} else if density > 0.2 {
		return '⠂'
	} else if density > 0.15 {
		return '⠁'
	} else if density > 0.1 {
		return '⠀'
	}
	return ' '
}

func densityToBlock(density float64) rune {
	// Unicode block elements
	if density > 1.0 {
		return '█' // Full block
	} else if density > 0.875 {
		return '▓' // Dark shade
	} else if density > 0.75 {
		return '▒' // Medium shade
	} else if density > 0.625 {
		return '░' // Light shade
	} else if density > 0.5 {
		return '▄' // Lower half block
	} else if density > 0.375 {
		return '▃' // Lower 3/8 block
	} else if density > 0.25 {
		return '▂' // Lower 1/4 block
	} else if density > 0.125 {
		return '▁' // Lower 1/8 block
	}
	return ' '
}

func densityToASCII(density float64) rune {
	// Original ASCII art characters
	if density > 1.0 {
		return '@'
	} else if density > 0.8 {
		return '#'
	} else if density > 0.6 {
		return '%'
	} else if density > 0.4 {
		return 'o'
	} else if density > 0.3 {
		return '='
	} else if density > 0.2 {
		return '+'
	} else if density > 0.15 {
		return '-'
	} else if density > 0.1 {
		return '.'
	} else if density > 0.05 {
		return '`'
	}
	return ' '
}

// ============================================================================
// ATTACK ARCS & TRAILS
// ============================================================================

type AttackArc struct {
	SrcLat    float64
	SrcLon    float64
	DstLat    float64
	DstLon    float64
	Protocol  string
	CreatedAt time.Time
	TTL       time.Duration // How long the arc persists
}

type ArcManager struct {
	arcs      []AttackArc
	arcStyle  string // "curved", "straight", "off"
	trailMS   int    // Trail persistence in milliseconds
	dstLat    float64
	dstLon    float64 // Default destination (honeypot location)
	mutex     sync.RWMutex
}

func NewArcManager(arcStyle string, trailMS int) *ArcManager {
	return &ArcManager{
		arcs:     make([]AttackArc, 0),
		arcStyle: arcStyle,
		trailMS:  trailMS,
		dstLat:   39.0997, // Kansas City (SecKC default)
		dstLon:   -94.5786,
	}
}

func (am *ArcManager) AddArc(srcLat, srcLon float64, protocol string) {
	am.mutex.Lock()
	defer am.mutex.Unlock()

	arc := AttackArc{
		SrcLat:    srcLat,
		SrcLon:    srcLon,
		DstLat:    am.dstLat,
		DstLon:    am.dstLon,
		Protocol:  protocol,
		CreatedAt: time.Now(),
		TTL:       time.Duration(am.trailMS) * time.Millisecond,
	}
	am.arcs = append(am.arcs, arc)
}

func (am *ArcManager) CleanupExpired() {
	am.mutex.Lock()
	defer am.mutex.Unlock()

	now := time.Now()
	validArcs := make([]AttackArc, 0)
	for _, arc := range am.arcs {
		if now.Sub(arc.CreatedAt) < arc.TTL {
			validArcs = append(validArcs, arc)
		}
	}
	am.arcs = validArcs
}

func (am *ArcManager) GetActiveArcs() []AttackArc {
	am.mutex.RLock()
	defer am.mutex.RUnlock()

	arcsCopy := make([]AttackArc, len(am.arcs))
	copy(arcsCopy, am.arcs)
	return arcsCopy
}

// Bezier curve calculation for curved arcs
func bezierPoint(t float64, p0, p1, p2, p3 float64) float64 {
	u := 1 - t
	return u*u*u*p0 + 3*u*u*t*p1 + 3*u*t*t*p2 + t*t*t*p3
}

// ============================================================================
// MATRIX RAIN EFFECT
// ============================================================================

type RainColumn struct {
	X         int
	Y         int
	Speed     float64
	Length    int
	Intensity float64
}

type MatrixRain struct {
	columns  []RainColumn
	enabled  bool
	density  int
	maxSpeed float64
	mutex    sync.RWMutex
}

func NewMatrixRain(width, height, density int) *MatrixRain {
	mr := &MatrixRain{
		columns:  make([]RainColumn, 0),
		enabled:  false,
		density:  density,
		maxSpeed: 1.5,
	}

	// Initialize rain columns based on density
	numColumns := (width * density) / 10
	for i := 0; i < numColumns; i++ {
		mr.columns = append(mr.columns, RainColumn{
			X:         rand.Intn(width),
			Y:         rand.Intn(height) - height,
			Speed:     0.3 + rand.Float64()*mr.maxSpeed,
			Length:    5 + rand.Intn(15),
			Intensity: 0.3 + rand.Float64()*0.7,
		})
	}

	return mr
}

func (mr *MatrixRain) Update() {
	mr.mutex.Lock()
	defer mr.mutex.Unlock()

	for i := range mr.columns {
		mr.columns[i].Y += int(mr.columns[i].Speed)
	}
}

func (mr *MatrixRain) SetEnabled(enabled bool) {
	mr.mutex.Lock()
	mr.enabled = enabled
	mr.mutex.Unlock()
}

// ============================================================================
// GLOBE RENDERING WITH ALL ENHANCEMENTS
// ============================================================================

type Globe struct {
	Radius       float64
	Width        int
	Height       int
	EarthMap     []string
	MapWidth     int
	MapHeight    int
	AspectRatio  float64
	Charset      Charset
	Lighting     bool
	LightLon     float64
	LightLat     float64
	LightFollow  bool
	Zoom         float64
	NudgeX       float64
	NudgeY       float64
}

func NewGlobe(width, height int, aspectRatio float64, charset Charset) *Globe {
	if width < 1 {
		width = 1
	}
	if height < 1 {
		height = 1
	}

	globeWidth := width
	effectiveHeight := float64(height) * aspectRatio
	radius := math.Min(float64(globeWidth)/2.5, effectiveHeight/2.5)

	if radius < 1.0 {
		radius = 1.0
	}

	earthMap := getEarthBitmap()
	return &Globe{
		Radius:      radius,
		Width:       globeWidth,
		Height:      height,
		EarthMap:    earthMap,
		MapWidth:    len(earthMap[0]),
		MapHeight:   len(earthMap),
		AspectRatio: aspectRatio,
		Charset:     charset,
		Lighting:    false,
		LightLon:    0,
		LightLat:    0,
		LightFollow: false,
		Zoom:        1.0,
		NudgeX:      0,
		NudgeY:      0,
	}
}

func (g *Globe) sampleEarthAt(lat, lon float64) rune {
	latNorm := (lat + 90) / 180
	lonNorm := (lon + 180) / 360

	y := int(latNorm * float64(g.MapHeight-1))
	x := int(lonNorm * float64(g.MapWidth-1))

	if y < 0 {
		y = 0
	}
	if y >= g.MapHeight {
		y = g.MapHeight - 1
	}
	if x < 0 {
		x = 0
	}
	if x >= g.MapWidth {
		x = g.MapWidth - 1
	}

	return rune(g.EarthMap[y][x])
}

func (g *Globe) project3DTo2D(lat, lon, rotation float64) (int, int, bool) {
	adjustedLon := -lon + 90
	adjustedLon = math.Mod(adjustedLon+180, 360) - 180
	latRad := lat * math.Pi / 180
	lonRad := (adjustedLon + rotation*180/math.Pi) * math.Pi / 180

	x := math.Cos(latRad) * math.Cos(lonRad)
	y := math.Sin(latRad)
	z := math.Cos(latRad) * math.Sin(lonRad)

	if z < 0 {
		return 0, 0, false
	}

	// Apply zoom and nudge
	effectiveRadius := g.Radius * g.Zoom
	screenX := int(x*effectiveRadius+g.NudgeX) + g.Width/2
	screenY := int(-y*effectiveRadius/g.AspectRatio+g.NudgeY) + g.Height/2

	if screenX < 0 || screenX >= g.Width || screenY < 0 || screenY >= g.Height {
		return 0, 0, false
	}

	return screenX, screenY, true
}

func (g *Globe) calculateLighting(lat, lon, rotation float64) float64 {
	if !g.Lighting {
		return 1.0
	}

	// Calculate light vector
	var lightLon, lightLat float64
	if g.LightFollow {
		// Light rotates opposite to globe
		lightLon = -rotation * 180 / math.Pi
		lightLat = 23.5 // Approximate Earth's axial tilt
	} else {
		lightLon = g.LightLon
		lightLat = g.LightLat
	}

	// Convert both point and light to 3D vectors
	adjustedLon := -lon + 90
	adjustedLon = math.Mod(adjustedLon+180, 360) - 180
	latRad := lat * math.Pi / 180
	lonRad := (adjustedLon + rotation*180/math.Pi) * math.Pi / 180

	// Surface normal at this point
	nx := math.Cos(latRad) * math.Cos(lonRad)
	ny := math.Sin(latRad)
	nz := math.Cos(latRad) * math.Sin(lonRad)

	// Light direction
	lightLatRad := lightLat * math.Pi / 180
	lightLonRad := lightLon * math.Pi / 180
	lx := math.Cos(lightLatRad) * math.Cos(lightLonRad)
	ly := math.Sin(lightLatRad)
	lz := math.Cos(lightLatRad) * math.Sin(lightLonRad)

	// Dot product for diffuse lighting (Lambertian)
	dotProduct := nx*lx + ny*ly + nz*lz
	intensity := math.Max(0.2, dotProduct) // Minimum ambient light 0.2

	return intensity
}

func (g *Globe) render(rotation float64, attackLocations map[string]LocationInfo, arcs []AttackArc, arcStyle string, protocolGlyphs bool) [][]rune {
	if g.Width <= 0 || g.Height <= 0 {
		return [][]rune{[]rune{' '}}
	}

	screen := make([][]rune, g.Height)
	for i := range screen {
		screen[i] = make([]rune, g.Width)
		for j := range screen[i] {
			screen[i][j] = ' '
		}
	}

	attackLayer := make([][]string, g.Height)
	for i := range attackLayer {
		attackLayer[i] = make([]string, g.Width)
	}

	// Render attack arcs if enabled
	if arcStyle != "off" && len(arcs) > 0 {
		for _, arc := range arcs {
			g.renderArc(arc, rotation, screen, arcStyle)
		}
	}

	// Render attack locations
	for ip, loc := range attackLocations {
		if loc.Valid {
			screenX, screenY, visible := g.project3DTo2D(loc.Latitude, loc.Longitude, rotation)
			if visible && screenX >= 0 && screenX < g.Width && screenY >= 0 && screenY < g.Height {
				// Store protocol for glyph rendering
				protocol := ""
				if protocolGlyphs {
					// Extract protocol from connection data
					protocol = getProtocolForIP(ip)
				}
				attackLayer[screenY][screenX] = protocol
			}
		}
	}

	density := make([][]float64, g.Height)
	for i := range density {
		density[i] = make([]float64, g.Width)
	}

	centerX, centerY := g.Width/2, g.Height/2
	effectiveRadius := g.Radius * g.Zoom

	for y := 0; y < g.Height; y++ {
		for x := 0; x < g.Width; x++ {
			dx := float64(x-centerX) - g.NudgeX
			dy := (float64(y-centerY) - g.NudgeY) * g.AspectRatio
			distance := math.Sqrt(dx*dx + dy*dy)

			if distance <= effectiveRadius {
				nx := dx / effectiveRadius
				ny := dy / effectiveRadius

				nz_squared := 1 - nx*nx - ny*ny
				if nz_squared >= 0 {
					nz := math.Sqrt(nz_squared)

					lat := math.Asin(ny) * 180 / math.Pi
					lon := math.Atan2(nx, nz)*180/math.Pi + rotation*180/math.Pi

					for lon < -180 {
						lon += 360
					}
					for lon > 180 {
						lon -= 360
					}

					earthChar := g.sampleEarthAt(lat, lon)
					if earthChar != ' ' {
						baseDensity := 1.0
						switch earthChar {
						case '#':
							baseDensity = 1.0
						case '.':
							baseDensity = 0.6
						default:
							baseDensity = 0.8
						}

						// Apply lighting
						lightFactor := g.calculateLighting(lat, lon, rotation)
						density[y][x] += baseDensity * lightFactor

						// Anti-aliasing
						for dy := -1; dy <= 1; dy++ {
							for dx := -1; dx <= 1; dx++ {
								nx2, ny2 := x+dx, y+dy
								if nx2 >= 0 && nx2 < g.Width && ny2 >= 0 && ny2 < g.Height {
									density[ny2][nx2] += 0.05 * lightFactor
								}
							}
						}
					}
				}
			}

			if distance > effectiveRadius-0.5 && distance < effectiveRadius+0.5 {
				density[y][x] += 0.2
			}
		}
	}

	// Convert density to characters
	for y := 0; y < g.Height; y++ {
		for x := 0; x < g.Width; x++ {
			d := density[y][x]
			screen[y][x] = densityToChar(d, g.Charset)

			// Overlay attack locations
			if attackLayer[y][x] != "" {
				protocol := attackLayer[y][x]
				if protocolGlyphs && protocol != "" {
					screen[y][x] = getProtocolGlyph(protocol)
				} else {
					screen[y][x] = '*'
				}
			}
		}
	}

	return screen
}

func (g *Globe) renderArc(arc AttackArc, rotation float64, screen [][]rune, arcStyle string) {
	age := time.Since(arc.CreatedAt)
	fadeFactor := 1.0 - (float64(age.Milliseconds()) / float64(arc.TTL.Milliseconds()))
	if fadeFactor < 0 {
		return
	}

	steps := 30
	for i := 0; i <= steps; i++ {
		t := float64(i) / float64(steps)

		var lat, lon float64
		if arcStyle == "curved" {
			// Bezier curve with control points for arc
			midLat := (arc.SrcLat + arc.DstLat) / 2
			midLon := (arc.SrcLon + arc.DstLon) / 2
			heightFactor := 20.0 // Arc height

			cp1Lat := arc.SrcLat + (midLat-arc.SrcLat)*0.5 + heightFactor
			cp1Lon := arc.SrcLon + (midLon - arc.SrcLon) * 0.5

			cp2Lat := midLat + (arc.DstLat-midLat)*0.5 + heightFactor
			cp2Lon := midLon + (arc.DstLon - midLon) * 0.5

			lat = bezierPoint(t, arc.SrcLat, cp1Lat, cp2Lat, arc.DstLat)
			lon = bezierPoint(t, arc.SrcLon, cp1Lon, cp2Lon, arc.DstLon)
		} else {
			// Straight line (great circle approximation)
			lat = arc.SrcLat + t*(arc.DstLat-arc.SrcLat)
			lon = arc.SrcLon + t*(arc.DstLon-arc.SrcLon)
		}

		screenX, screenY, visible := g.project3DTo2D(lat, lon, rotation)
		if visible && screenX >= 0 && screenX < g.Width && screenY >= 0 && screenY < g.Height {
			// Trail fade: newer parts brighter
			segmentFade := fadeFactor * (0.3 + 0.7*t)
			if segmentFade > 0.3 && screen[screenY][screenX] == ' ' {
				screen[screenY][screenX] = '·'
			}
		}
	}
}

func getProtocolGlyph(protocol string) rune {
	switch strings.ToLower(protocol) {
	case "ssh":
		return '#'
	case "telnet":
		return '~'
	case "smtp":
		return '@'
	case "http", "https":
		return ':'
	case "ftp":
		return '%'
	default:
		return '!'
	}
}

func getProtocolForIP(ip string) string {
	// Look up protocol from global dashboard
	if globalTUI != nil && globalTUI.dashboard != nil {
		globalTUI.dashboard.mutex.RLock()
		defer globalTUI.dashboard.mutex.RUnlock()
		for _, conn := range globalTUI.dashboard.Connections {
			if conn.IP == ip {
				return conn.Protocol
			}
		}
	}
	return ""
}

// ============================================================================
// CRT EFFECTS
// ============================================================================

type CRTEffect struct {
	enabled      bool
	glowLevel    int
	phosphorBuf  [][]float64
	scanlineShad float64
}

func NewCRTEffect(width, height int) *CRTEffect {
	buf := make([][]float64, height)
	for i := range buf {
		buf[i] = make([]float64, width)
	}
	return &CRTEffect{
		enabled:      false,
		glowLevel:    0,
		phosphorBuf:  buf,
		scanlineShad: 0.7,
	}
}

func (crt *CRTEffect) ApplyGlow(screen [][]rune, x, y int) {
	if !crt.enabled || crt.glowLevel == 0 {
		return
	}

	crt.phosphorBuf[y][x] = 1.0
}

func (crt *CRTEffect) Update() {
	if !crt.enabled {
		return
	}

	// Decay phosphor glow
	decay := 0.85
	for y := range crt.phosphorBuf {
		for x := range crt.phosphorBuf[y] {
			crt.phosphorBuf[y][x] *= decay
		}
	}
}

// ============================================================================
// TUI STATE & CONTROLS
// ============================================================================

type TUIState struct {
	paused          bool
	spinSpeed       float64
	showHelp        bool
	showGrid        bool
	showArcs        bool
	showInfo        bool   // Show detailed info panel
	showStats       bool   // Show top attackers stats
	showTopIPs      bool   // Show top IP addresses panel
	showCommands    bool   // Show command guide
	savedArcStyle   string // Remember the arc style when toggling
	currentTheme    int
	dashboardScroll int    // Horizontal scroll offset for dashboard
	mutex           sync.RWMutex
}

func NewTUIState() *TUIState {
	return &TUIState{
		paused:       false,
		spinSpeed:    1.0,
		showHelp:     false,
		showGrid:     false,
		showArcs:     true,
		currentTheme: 0,
	}
}

// ============================================================================
// DEMO STORM GENERATOR
// ============================================================================

type DemoStorm struct {
	enabled  bool
	asn      int
	rate     int
	active   bool
	stopChan chan bool
}

func NewDemoStorm() *DemoStorm {
	return &DemoStorm{
		enabled:  false,
		stopChan: make(chan bool),
	}
}

func (ds *DemoStorm) Start(dashboard *Dashboard) {
	if !ds.enabled || ds.active {
		return
	}

	ds.active = true
	go func() {
		ticker := time.NewTicker(time.Second / time.Duration(ds.rate))
		defer ticker.Stop()

		for {
			select {
			case <-ds.stopChan:
				return
			case <-ticker.C:
				ip := generateRandomIP()
				username := generateRandomUsername()
				password := generateRandomPassword()
				protocol := randomProtocol()
				dashboard.AddConnection(ip, username, password, protocol)
			}
		}
	}()
}

func (ds *DemoStorm) Stop() {
	if ds.active {
		ds.stopChan <- true
		ds.active = false
	}
}

func randomProtocol() string {
	protocols := []string{"ssh", "telnet", "http", "ftp", "smtp"}
	return protocols[rand.Intn(len(protocols))]
}

// ============================================================================
// ASCIINEMA RECORDING
// ============================================================================

type AsciinemaRecorder struct {
	enabled   bool
	file      *os.File
	startTime time.Time
	width     int
	height    int
}

func NewAsciinemaRecorder(filepath string, width, height int) (*AsciinemaRecorder, error) {
	if filepath == "" {
		return &AsciinemaRecorder{enabled: false}, nil
	}

	file, err := os.Create(filepath)
	if err != nil {
		return nil, err
	}

	recorder := &AsciinemaRecorder{
		enabled:   true,
		file:      file,
		startTime: time.Now(),
		width:     width,
		height:    height,
	}

	// Write asciinema v2 header
	header := map[string]interface{}{
		"version":   2,
		"width":     width,
		"height":    height,
		"timestamp": time.Now().Unix(),
		"env": map[string]string{
			"TERM":  "xterm-256color",
			"SHELL": "/bin/bash",
		},
	}
	headerJSON, _ := json.Marshal(header)
	file.Write(headerJSON)
	file.Write([]byte("\n"))

	return recorder, nil
}

func (ar *AsciinemaRecorder) RecordFrame(screen [][]rune) {
	if !ar.enabled {
		return
	}

	// Convert screen to string
	var sb strings.Builder
	for _, row := range screen {
		sb.WriteString(string(row))
		sb.WriteRune('\n')
	}

	timestamp := time.Since(ar.startTime).Seconds()
	event := []interface{}{timestamp, "o", sb.String()}
	eventJSON, _ := json.Marshal(event)
	ar.file.Write(eventJSON)
	ar.file.Write([]byte("\n"))
}

func (ar *AsciinemaRecorder) Close() {
	if ar.enabled && ar.file != nil {
		ar.file.Close()
	}
}

// ============================================================================
// CONFIG FILE SUPPORT
// ============================================================================

type Config struct {
	API struct {
		BaseURL      string `toml:"base_url"`
		PollInterval string `toml:"poll_interval"`
		MaxEvents    int    `toml:"max_events"`
	} `toml:"api"`

	Display struct {
		Theme          string  `toml:"theme"`
		Charset        string  `toml:"charset"`
		RotationPeriod int     `toml:"rotation_period"`
		RefreshRate    int     `toml:"refresh_rate"`
		AspectRatio    float64 `toml:"aspect_ratio"`
	} `toml:"display"`

	Effects struct {
		ArcStyle    string `toml:"arc_style"`
		TrailMS     int    `toml:"trail_ms"`
		CRTEnabled  bool   `toml:"crt_enabled"`
		GlowLevel   int    `toml:"glow_level"`
		RainEnabled bool   `toml:"rain_enabled"`
		RainDensity int    `toml:"rain_density"`
	} `toml:"effects"`

	Lighting struct {
		Enabled bool    `toml:"enabled"`
		Lon     float64 `toml:"lon"`
		Lat     float64 `toml:"lat"`
		Follow  bool    `toml:"follow"`
	} `toml:"lighting"`
}

func LoadConfig(path string) (*Config, error) {
	var config Config

	if path == "" {
		return &config, nil
	}

	_, err := toml.DecodeFile(path, &config)
	if err != nil {
		return nil, err
	}

	return &config, nil
}

// ============================================================================
// GLOBAL VARIABLES & EXISTING FUNCTIONS (adapted)
// ============================================================================

var debugLogger *log.Logger
var globalGeoIP *GeoIPManager
var globalAPIConnected bool
var globalGeoIPAvailable bool
var lastProcessedEventTime float64
var globalTUI *TUI
var globalArcManager *ArcManager
var globalDemoStorm *DemoStorm

type TUI struct {
	screen       tcell.Screen
	width        int
	height       int
	globe        *Globe
	dashboard    *Dashboard
	stats        *StatsManager
	state        *TUIState
	rain         *MatrixRain
	crt          *CRTEffect
	recorder     *AsciinemaRecorder
	globeChanged bool
	dashChanged  bool
	statsChanged bool
	mutex        sync.RWMutex
}

func debugLog(format string, v ...interface{}) {
	if debugLogger != nil {
		debugLogger.Printf(format, v...)
	}
}

func NewAPIClient(config *APIConfig) *APIClient {
	return &APIClient{
		config: config,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		lastEventTS: 0,
	}
}

func NewGeoIPManager(apiClient *APIClient) *GeoIPManager {
	return &GeoIPManager{
		apiClient: apiClient,
		cache:     make(map[string]GeocodeCache),
		cacheList: make([]string, 0),
		maxCache:  2000,
	}
}

func (g *GeoIPManager) LookupIP(ipStr string) LocationInfo {
	g.mutex.RLock()
	if cached, exists := g.cache[ipStr]; exists {
		g.mutex.RUnlock()
		debugLog("Geocode Cache: Hit for %s", ipStr)
		g.moveToFront(ipStr)
		return cached.Location
	}
	g.mutex.RUnlock()

	debugLog("Geocode Cache: Miss for %s", ipStr)
	location := g.fetchFromAPI(ipStr)

	if location.Valid {
		g.addToCache(ipStr, location)
	}

	return location
}

func (g *GeoIPManager) fetchFromAPI(ipStr string) LocationInfo {
	if g.apiClient == nil {
		return LocationInfo{Valid: false}
	}

	url := fmt.Sprintf("%s/geocode/%s", strings.TrimSuffix(g.apiClient.config.BaseURL, "/"), ipStr)
	resp, err := g.apiClient.httpClient.Get(url)
	if err != nil {
		debugLog("Geocode API: Failed %s: %v", ipStr, err)
		return LocationInfo{Valid: false}
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		debugLog("Geocode API: Status %d for %s", resp.StatusCode, ipStr)
		return LocationInfo{Valid: false}
	}

	var geocodeResp GeocodeResponse
	if err := json.NewDecoder(resp.Body).Decode(&geocodeResp); err != nil {
		debugLog("Geocode API: Decode failed for %s: %v", ipStr, err)
		return LocationInfo{Valid: false}
	}

	// Skip ASN/rDNS lookups in demo mode for performance
	var asn, org, rdns string
	if globalDemoStorm == nil || !globalDemoStorm.enabled {
		// Only fetch ASN/rDNS for real (non-demo) traffic
		asn, org = g.lookupASN(ipStr)
		rdns = g.lookupReverseDNS(ipStr)
	}

	return LocationInfo{
		City:      geocodeResp.City.Names["en"],
		Country:   geocodeResp.Country.Names["en"],
		Latitude:  geocodeResp.Location.Latitude,
		Longitude: geocodeResp.Location.Longitude,
		ASN:       asn,
		Org:       org,
		RDNS:      rdns,
		Valid:     true,
	}
}

func (g *GeoIPManager) lookupASN(ipStr string) (string, string) {
	// Try to fetch ASN info from ipinfo.io API (free tier allows limited requests)
	url := fmt.Sprintf("https://ipinfo.io/%s/json", ipStr)

	client := &http.Client{Timeout: 2 * time.Second} // Increased timeout
	resp, err := client.Get(url)
	if err != nil {
		debugLog("ASN Lookup: Failed for %s: %v", ipStr, err)
		return "", ""
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		debugLog("ASN Lookup: HTTP %d for %s", resp.StatusCode, ipStr)
		return "", ""
	}

	var result struct {
		Org string `json:"org"` // Format: "AS15169 Google LLC"
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		debugLog("ASN Lookup: Decode error for %s: %v", ipStr, err)
		return "", ""
	}

	// Parse "AS15169 Google LLC" into ASN and Org
	if result.Org != "" {
		debugLog("ASN Lookup: Success for %s: %s", ipStr, result.Org)
		parts := strings.SplitN(result.Org, " ", 2)
		if len(parts) == 2 {
			return parts[0], parts[1] // "AS15169", "Google LLC"
		}
		return "", result.Org
	}

	return "", ""
}

func (g *GeoIPManager) lookupReverseDNS(ipStr string) string {
	// Perform reverse DNS lookup with timeout
	// Use goroutine with timeout to prevent blocking
	resultChan := make(chan []string, 1)

	go func() {
		names, err := net.LookupAddr(ipStr)
		if err == nil && len(names) > 0 {
			debugLog("rDNS Lookup: Success for %s: %s", ipStr, names[0])
			resultChan <- names
		} else {
			debugLog("rDNS Lookup: Failed for %s: %v", ipStr, err)
			resultChan <- nil
		}
	}()

	// Wait up to 1 second for result (increased from 300ms)
	select {
	case names := <-resultChan:
		if names != nil && len(names) > 0 {
			// Return the first reverse DNS name, remove trailing dot
			rdns := strings.TrimSuffix(names[0], ".")
			debugLog("rDNS Lookup: Returning %s for %s", rdns, ipStr)
			return rdns
		}
	case <-time.After(1000 * time.Millisecond):
		debugLog("rDNS Lookup: Timeout for %s", ipStr)
	}

	return ""
}

func (g *GeoIPManager) addToCache(ipStr string, location LocationInfo) {
	g.mutex.Lock()
	defer g.mutex.Unlock()

	if len(g.cache) >= g.maxCache {
		g.evictOldest()
	}

	g.cache[ipStr] = GeocodeCache{
		IP:        ipStr,
		Location:  location,
		Timestamp: time.Now(),
	}

	g.cacheList = append([]string{ipStr}, g.cacheList...)
}

func (g *GeoIPManager) moveToFront(ipStr string) {
	g.mutex.Lock()
	defer g.mutex.Unlock()

	for i, ip := range g.cacheList {
		if ip == ipStr {
			g.cacheList = append(g.cacheList[:i], g.cacheList[i+1:]...)
			break
		}
	}

	g.cacheList = append([]string{ipStr}, g.cacheList...)
}

func (g *GeoIPManager) evictOldest() {
	if len(g.cacheList) == 0 {
		return
	}

	oldestIP := g.cacheList[len(g.cacheList)-1]
	delete(g.cache, oldestIP)
	g.cacheList = g.cacheList[:len(g.cacheList)-1]
}

func (g *GeoIPManager) GetCacheStats() (int, int) {
	g.mutex.RLock()
	defer g.mutex.RUnlock()
	return len(g.cache), g.maxCache
}

func (api *APIClient) GetRecentEvents() ([]APIEvent, error) {
	url := fmt.Sprintf("%s/feeds/events/recent", strings.TrimSuffix(api.config.BaseURL, "/"))

	if api.lastEventTS > 0 {
		url = fmt.Sprintf("%s?since=%.1f&limit=%d", url, api.lastEventTS, api.config.MaxEvents)
	} else {
		url = fmt.Sprintf("%s?limit=%d", url, api.config.MaxEvents)
	}

	resp, err := api.httpClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to get events: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("API request failed: status %d", resp.StatusCode)
	}

	var apiResp APIResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %v", err)
	}

	if len(apiResp.Events) > 0 {
		api.lastEventTS = apiResp.Events[len(apiResp.Events)-1].Timestamp
	}

	return apiResp.Events, nil
}

func NewStatsManager() *StatsManager {
	return &StatsManager{}
}

func (s *StatsManager) updateURLs() {
	now := time.Now()
	today := now.Format("20060102")
	yesterday := now.AddDate(0, 0, -1).Format("20060102")

	s.todayURL = fmt.Sprintf("https://mhn.h-i-r.net/seckcapi/stats/attacks?date=%s", today)
	s.yesterdayURL = fmt.Sprintf("https://mhn.h-i-r.net/seckcapi/stats/attacks?date=%s", yesterday)
}

func (s *StatsManager) fetchFromURL(url, label string) (StatsResponse, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var stats StatsResponse
	if err := json.Unmarshal(body, &stats); err != nil {
		return nil, err
	}

	return stats, nil
}

func (s *StatsManager) FetchData() error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	s.updateURLs()

	if time.Since(s.lastFetch) < 5*time.Minute && len(s.todayData) > 0 {
		return nil
	}

	todayData, _ := s.fetchFromURL(s.todayURL, "Today")
	s.todayData = todayData

	yesterdayData, _ := s.fetchFromURL(s.yesterdayURL, "Yesterday")
	s.yesterdayData = yesterdayData

	if len(s.todayData) > 0 || len(s.yesterdayData) > 0 {
		s.lastFetch = time.Now()
		return nil
	}

	return fmt.Errorf("no data available")
}

func (s *StatsManager) GetHourlyData() map[string]int {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	rollingData := make(map[string]int)
	currentHour := time.Now().Hour()

	for i := 0; i < 24; i++ {
		targetHour := (currentHour - i + 24) % 24
		targetHourStr := fmt.Sprintf("%d", targetHour)

		var count int
		if i <= currentHour && len(s.todayData) > 0 {
			count, _ = s.todayData[0].Hourly[targetHourStr]
		} else if len(s.yesterdayData) > 0 {
			count, _ = s.yesterdayData[0].Hourly[targetHourStr]
		}

		rollingKey := fmt.Sprintf("%d", 23-i)
		rollingData[rollingKey] = count
	}

	return rollingData
}

func (s *StatsManager) RenderBarGraph(width int) []string {
	hourlyData := s.GetHourlyData()

	if len(hourlyData) == 0 {
		return []string{"", "", ""}
	}

	maxVal := 0
	for _, count := range hourlyData {
		if count > maxVal {
			maxVal = count
		}
	}

	if maxVal == 0 {
		return []string{"", "", ""}
	}

	lines := make([]string, 3)
	chartWidth := 24
	maxValStr := fmt.Sprintf("%d", maxVal)
	labelWidth := len(maxValStr) + 1

	for lineIdx := 0; lineIdx < 3; lineIdx++ {
		var line string

		if lineIdx == 0 {
			line = fmt.Sprintf("%*s ", labelWidth-1, maxValStr)
		} else if lineIdx == 2 {
			line = fmt.Sprintf("%*s ", labelWidth-1, "0")
		} else {
			line = fmt.Sprintf("%*s ", labelWidth-1, "")
		}

		for pos := 0; pos < chartWidth && pos < 24; pos++ {
			posStr := fmt.Sprintf("%d", pos)
			count, exists := hourlyData[posStr]
			if !exists {
				count = 0
			}

			normalizedHeight := float64(count) / float64(maxVal) * 3.0
			lineHeight := 3 - lineIdx

			var barChar rune
			if normalizedHeight >= float64(lineHeight) {
				barChar = '#'
			} else if normalizedHeight >= float64(lineHeight-1) {
				remainder := normalizedHeight - float64(lineHeight-1)
				if remainder >= 0.66 {
					barChar = '#'
				} else if remainder >= 0.33 {
					barChar = '='
				} else if remainder > 0 {
					barChar = '_'
				} else {
					barChar = ' '
				}
			} else {
				barChar = ' '
			}

			line += string(barChar)
		}
		lines[lineIdx] = line
	}

	return lines
}

func (s *StatsManager) RenderSparkline() string {
	hourlyData := s.GetHourlyData()

	if len(hourlyData) == 0 {
		return ""
	}

	maxVal := 0
	for _, count := range hourlyData {
		if count > maxVal {
			maxVal = count
		}
	}

	if maxVal == 0 {
		return strings.Repeat("▁", 24)
	}

	sparkChars := []rune{'▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}
	var sparkline strings.Builder

	for pos := 0; pos < 24; pos++ {
		posStr := fmt.Sprintf("%d", pos)
		count, exists := hourlyData[posStr]
		if !exists {
			count = 0
		}

		normalized := float64(count) / float64(maxVal)
		charIdx := int(normalized * float64(len(sparkChars)-1))
		sparkline.WriteRune(sparkChars[charIdx])
	}

	return sparkline.String()
}

func NewDashboard(maxLines int) *Dashboard {
	return &Dashboard{
		Connections: make([]Connection, 0),
		MaxLines:    maxLines,
	}
}

func (d *Dashboard) AddConnection(ip, username, password, protocol string) {
	if d == nil {
		return
	}

	d.mutex.Lock()
	defer d.mutex.Unlock()

	// Create connection with basic info first (fast)
	connection := Connection{
		IP:       ip,
		Username: username,
		Password: password,
		Protocol: protocol,
		Time:     time.Now(),
	}

	// Lookup geolocation for arc rendering (fast, cached)
	if globalGeoIP != nil {
		loc := globalGeoIP.LookupIP(ip)
		if loc.Valid {
			connection.City = loc.City
			connection.Country = loc.Country
			connection.ASN = loc.ASN
			connection.Org = loc.Org
			connection.RDNS = loc.RDNS
			// Add to arc manager if enabled
			if globalArcManager != nil {
				globalArcManager.AddArc(loc.Latitude, loc.Longitude, protocol)
			}
		}
	}

	d.Connections = append(d.Connections, connection)

	if len(d.Connections) > d.MaxLines {
		d.Connections = d.Connections[len(d.Connections)-d.MaxLines:]
	}

	if globalTUI != nil {
		globalTUI.MarkDashboardChanged()
	}
}

func (d *Dashboard) GenerateRandomConnection() {
	ip := generateRandomIP()
	username := generateRandomUsername()
	password := generateRandomPassword()
	protocol := randomProtocol()

	// Add with basic info - geolocation will be looked up in AddConnection
	d.AddConnection(ip, username, password, protocol)
}

func (d *Dashboard) Render(height int, width int) []string {
	d.mutex.RLock()
	defer d.mutex.RUnlock()

	lines := make([]string, height)

	// Single header line with all fields
	headerLine := "IP              [CC] City         Prot User:Pass  Time  ASN / Org / rDNS"
	if len(headerLine) > width {
		headerLine = headerLine[:width]
	}
	lines[0] = headerLine
	lines[1] = strings.Repeat("-", width)

	startLine := 2
	for i, conn := range d.Connections {
		lineIdx := startLine + i // Single line per connection
		if lineIdx >= height {
			break
		}

		// Extract country code
		countryCode := ""
		if conn.Country != "" {
			parts := strings.Fields(conn.Country)
			if len(parts) > 0 {
				countryCode = "[" + parts[0][:min(2, len(parts[0]))] + "]"
			}
		}

		// City (no truncation - show full city name)
		city := conn.City
		if city == "" {
			city = "Unknown"
		}

		// Protocol (4 chars)
		proto := conn.Protocol
		if len(proto) > 4 {
			proto = proto[:4]
		}

		// Credentials (no truncation - show full username:password)
		credPart := fmt.Sprintf("%s:%s", conn.Username, conn.Password)

		// Time (HH:MM)
		timeStr := conn.Time.Format("15:04")

		// ASN/Org or rDNS info (use all remaining width)
		var enrichInfo string
		if conn.Org != "" {
			enrichInfo = fmt.Sprintf("%s %s", conn.ASN, conn.Org)
		} else if conn.RDNS != "" {
			enrichInfo = conn.RDNS
		} else {
			enrichInfo = "..."
		}

		// Format: IP [CC] City Proto User:Pass Time ASN/Org/rDNS (all on one line)
		line := fmt.Sprintf("%-15s %s %-12s %-4s %-10s %-5s %s",
			conn.IP, countryCode, city, proto, credPart, timeStr, enrichInfo)

		// Only truncate if line is significantly longer than width (allows some overflow)
		if len(line) > width+10 {
			line = line[:width-1] + "»" // Use » to indicate more text
		}
		lines[lineIdx] = line
	}

	for i := startLine + len(d.Connections); i < height; i++ {
		lines[i] = ""
	}

	return lines
}

func generateRandomIP() string {
	return fmt.Sprintf("%d.%d.%d.%d",
		rand.Intn(256), rand.Intn(256), rand.Intn(256), rand.Intn(256))
}

func generateRandomUsername() string {
	usernames := []string{
		"admin", "root", "user", "guest", "test", "demo", "backup", "service",
		"operator", "manager", "support", "dev", "prod", "staging", "www",
	}
	return usernames[rand.Intn(len(usernames))]
}

func generateRandomPassword() string {
	passwords := []string{
		"123456", "password", "admin", "root", "guest", "test", "demo",
		"letmein", "welcome", "monkey", "dragon", "qwerty", "abc123",
	}
	return passwords[rand.Intn(len(passwords))]
}

func createAPIConfig(baseURL string, pollInterval time.Duration, maxEvents int) *APIConfig {
	return &APIConfig{
		BaseURL:      baseURL,
		PollInterval: pollInterval,
		MaxEvents:    maxEvents,
	}
}

func startAPIClient(apiClient *APIClient, dashboard *Dashboard) error {
	go func() {
		ticker := time.NewTicker(apiClient.config.PollInterval)
		defer ticker.Stop()

		for {
			<-ticker.C
			events, err := apiClient.GetRecentEvents()
			if err != nil {
				globalAPIConnected = false
				continue
			}

			globalAPIConnected = true

			for _, apiEvent := range events {
				if apiEvent.Timestamp <= lastProcessedEventTime {
					continue
				}

				if apiEvent.Timestamp > lastProcessedEventTime {
					lastProcessedEventTime = apiEvent.Timestamp
				}

				eventData := apiEvent.Event

				var ipAddress string
				if srcIP, ok := eventData["src_ip"].(string); ok {
					ipAddress = srcIP
				} else if peerIP, ok := eventData["peerIP"].(string); ok {
					ipAddress = peerIP
				}

				if ipAddress == "" {
					continue
				}

				var username, password, protocol string
				if loggedin, ok := eventData["loggedin"].([]interface{}); ok && len(loggedin) >= 2 {
					if user, ok := loggedin[0].(string); ok {
						username = user
					}
					if pass, ok := loggedin[1].(string); ok {
						password = pass
					}
				}

				if username == "" {
					if user, ok := eventData["username"].(string); ok {
						username = user
					}
				}
				if password == "" {
					if pass, ok := eventData["password"].(string); ok {
						password = pass
					}
				}

				if proto, ok := eventData["protocol"].(string); ok {
					protocol = proto
				}

				if username == "" && password == "" {
					if protocol != "" {
						username = "connection"
						password = protocol
					}
				}

				if username == "" {
					username = "unknown"
				}
				if password == "" {
					password = "unknown"
				}

				dashboard.AddConnection(ipAddress, username, password, protocol)
			}
		}
	}()

	return nil
}

func NewTUI(aspectRatio float64, charset Charset, recordPath string) (*TUI, error) {
	screen, err := tcell.NewScreen()
	if err != nil {
		return nil, err
	}

	if err := screen.Init(); err != nil {
		return nil, err
	}

	screen.SetStyle(tcell.StyleDefault.Background(currentTheme.Background).Foreground(currentTheme.Text))
	screen.Clear()

	width, height := screen.Size()

	recorder, err := NewAsciinemaRecorder(recordPath, width, height)
	if err != nil {
		debugLog("Failed to initialize recorder: %v", err)
		recorder = &AsciinemaRecorder{enabled: false}
	}

	tui := &TUI{
		screen:       screen,
		width:        width,
		height:       height,
		state:        NewTUIState(),
		rain:         NewMatrixRain(width, height, 5),
		crt:          NewCRTEffect(width, height),
		recorder:     recorder,
		globeChanged: true,
		dashChanged:  true,
		statsChanged: true,
	}

	// Dynamic dashboard width: 50% of terminal, minimum 45, maximum 80
	dashboardWidth := width / 2
	if dashboardWidth < 45 {
		dashboardWidth = 45
	}
	if dashboardWidth > 80 {
		dashboardWidth = 80
	}

	globeWidth := width - dashboardWidth - 3
	if globeWidth < 10 {
		globeWidth = 10
	}

	tui.globe = NewGlobe(globeWidth, height, aspectRatio, charset)
	tui.dashboard = NewDashboard(height - 4)
	tui.stats = NewStatsManager()

	return tui, nil
}

func (tui *TUI) Close() {
	if tui.recorder != nil {
		tui.recorder.Close()
	}
	if tui.screen != nil {
		tui.screen.Fini()
	}
}

func (tui *TUI) HandleResize(aspectRatio float64) {
	// Get new size first (no lock needed for screen operations)
	newWidth, newHeight := tui.screen.Size()

	tui.mutex.Lock()
	tui.width = newWidth
	tui.height = newHeight
	tui.mutex.Unlock()

	// Minimum size check
	if newWidth < 60 || newHeight < 20 {
		tui.screen.Clear()
		tui.screen.Show()
		return
	}

	// Calculate globe width - globe gets more space to expand
	// Globe takes 60% of width, dashboard gets 40%
	globeWidth := (newWidth * 60) / 100
	if globeWidth < 60 {
		globeWidth = 60
	}
	if globeWidth > 200 {
		globeWidth = 200
	}

	// Preserve and recreate globe
	tui.mutex.Lock()
	if tui.globe != nil {
		charset := tui.globe.Charset
		lighting := tui.globe.Lighting
		lightLon := tui.globe.LightLon
		lightLat := tui.globe.LightLat
		lightFollow := tui.globe.LightFollow
		zoom := tui.globe.Zoom
		nudgeX := tui.globe.NudgeX
		nudgeY := tui.globe.NudgeY

		tui.globe = NewGlobe(globeWidth, newHeight, aspectRatio, charset)
		tui.globe.Lighting = lighting
		tui.globe.LightLon = lightLon
		tui.globe.LightLat = lightLat
		tui.globe.LightFollow = lightFollow
		tui.globe.Zoom = zoom
		tui.globe.NudgeX = nudgeX
		tui.globe.NudgeY = nudgeY
	}

	// Recreate rain
	if tui.rain != nil {
		rainEnabled := tui.rain.enabled
		rainDensity := tui.rain.density
		tui.rain = NewMatrixRain(newWidth, newHeight, rainDensity)
		tui.rain.enabled = rainEnabled
	}

	// Recreate CRT
	if tui.crt != nil {
		crtEnabled := tui.crt.enabled
		glowLevel := tui.crt.glowLevel
		tui.crt = NewCRTEffect(newWidth, newHeight)
		tui.crt.enabled = crtEnabled
		tui.crt.glowLevel = glowLevel
	}
	tui.mutex.Unlock()

	// Update dashboard
	if tui.dashboard != nil {
		tui.dashboard.mutex.Lock()
		newMaxLines := newHeight - 4
		if newMaxLines < 1 {
			newMaxLines = 1
		}
		tui.dashboard.MaxLines = newMaxLines
		if len(tui.dashboard.Connections) > newMaxLines {
			tui.dashboard.Connections = tui.dashboard.Connections[len(tui.dashboard.Connections)-newMaxLines:]
		}
		tui.dashboard.mutex.Unlock()
	}

	// Clear and mark for redraw
	tui.screen.Clear()
	tui.MarkGlobeChanged()
	tui.MarkDashboardChanged()
	tui.MarkStatsChanged()
	tui.screen.Show()
}

func (tui *TUI) MarkGlobeChanged() {
	tui.mutex.Lock()
	tui.globeChanged = true
	tui.mutex.Unlock()
}

func (tui *TUI) MarkDashboardChanged() {
	tui.mutex.Lock()
	tui.dashChanged = true
	tui.mutex.Unlock()
}

func (tui *TUI) MarkStatsChanged() {
	tui.mutex.Lock()
	tui.statsChanged = true
	tui.mutex.Unlock()
}

func (tui *TUI) drawText(x, y int, text string, style tcell.Style) {
	// Bounds check
	if y < 0 || y >= tui.height || x >= tui.width {
		return
	}

	for i, r := range []rune(text) {
		if x+i < 0 {
			continue
		}
		// Don't break at screen width - let tcell handle it
		// This allows text to extend beyond the terminal if needed
		if x+i < tui.width {
			tui.screen.SetContent(x+i, y, r, nil, style)
		}
	}
}

func (tui *TUI) renderGlobe(rotation float64, protocolGlyphs bool) {
	tui.mutex.RLock()
	changed := tui.globeChanged
	tui.mutex.RUnlock()

	if !changed {
		return
	}

	// Collect attack locations
	attackLocations := make(map[string]LocationInfo)
	if globalGeoIP != nil && tui.dashboard != nil {
		tui.dashboard.mutex.RLock()
		for _, conn := range tui.dashboard.Connections {
			if _, exists := attackLocations[conn.IP]; !exists {
				loc := globalGeoIP.LookupIP(conn.IP)
				if loc.Valid {
					attackLocations[conn.IP] = loc
				}
			}
		}
		tui.dashboard.mutex.RUnlock()
	}

	// Get active arcs
	var arcs []AttackArc
	arcStyle := "off"
	if globalArcManager != nil {
		arcs = globalArcManager.GetActiveArcs()
		arcStyle = globalArcManager.arcStyle
	}

	globeScreen := tui.globe.render(rotation, attackLocations, arcs, arcStyle, protocolGlyphs)

	// Apply theme colors
	landStyle := tcell.StyleDefault.Foreground(currentTheme.Globe)
	attackStyle := tcell.StyleDefault.Foreground(currentTheme.Attack).Bold(true)
	glyphStyle := tcell.StyleDefault.Foreground(currentTheme.AttackGlyph).Bold(true)

	// Rainbow and Skittles modes: colorful globe characters
	rainbowMode := currentTheme.Name == "rainbow"
	skittlesMode := currentTheme.Name == "skittles"
	rainbowColors := []tcell.Color{
		tcell.NewRGBColor(255, 0, 0),     // Red
		tcell.NewRGBColor(255, 127, 0),   // Orange
		tcell.NewRGBColor(255, 255, 0),   // Yellow
		tcell.NewRGBColor(0, 255, 0),     // Green
		tcell.NewRGBColor(0, 0, 255),     // Blue
		tcell.NewRGBColor(75, 0, 130),    // Indigo
		tcell.NewRGBColor(148, 0, 211),   // Violet
	}

	// Clear globe area with bounds checking
	for y := 0; y < tui.globe.Height && y < tui.height; y++ {
		for x := 0; x < tui.globe.Width && x < tui.width; x++ {
			tui.screen.SetContent(x, y, ' ', nil, tcell.StyleDefault)
		}
	}

	// Render matrix rain if enabled
	if tui.rain != nil && tui.rain.enabled {
		tui.rain.mutex.RLock()
		for _, col := range tui.rain.columns {
			if col.X >= 0 && col.X < tui.globe.Width && col.X < tui.width &&
			   col.Y >= 0 && col.Y < tui.globe.Height && col.Y < tui.height {
				rainStyle := tcell.StyleDefault.Foreground(currentTheme.RainEffect)
				tui.screen.SetContent(col.X, col.Y, '|', nil, rainStyle)
			}
		}
		tui.rain.mutex.RUnlock()
	}

	// Draw globe with strict bounds checking
	for y := 0; y < len(globeScreen) && y < tui.height && y < tui.globe.Height; y++ {
		for x := 0; x < len(globeScreen[y]) && x < tui.globe.Width && x < tui.width; x++ {
			char := globeScreen[y][x]
			if char != ' ' {
				style := landStyle

				// Check for attacks and protocol glyphs first
				isAttack := (char == '*' || char == '·')
				isGlyph := protocolGlyphs && (char == '#' || char == '~' || char == '@' || char == ':' || char == '%' || char == '!')

				if isGlyph {
					style = glyphStyle
				} else if isAttack {
					style = attackStyle
				} else if rainbowMode {
					// Rainbow mode: solid rainbow pattern (diagonal stripes)
					colorIdx := (x + y) % len(rainbowColors)
					style = tcell.StyleDefault.Foreground(rainbowColors[colorIdx])
				} else if skittlesMode {
					// Skittles mode: randomized rainbow colors for each character
					// Use position as seed for pseudo-random but consistent colors per position
					colorIdx := ((x * 73) + (y * 37)) % len(rainbowColors)
					style = tcell.StyleDefault.Foreground(rainbowColors[colorIdx])
				}

				// CRT scanline effect
				if tui.crt != nil && tui.crt.enabled && y%2 == 0 {
					// Dim every other line for scanline effect
					fg, bg, attr := style.Decompose()
					// Can't easily dim in tcell, so we'll use the theme's scanline shade factor
					// This is a simplified version
					style = tcell.StyleDefault.Foreground(fg).Background(bg).Attributes(attr)
				}

				tui.screen.SetContent(x, y, char, nil, style)
			}
		}
	}

	tui.mutex.Lock()
	tui.globeChanged = false
	tui.mutex.Unlock()
}

func (tui *TUI) renderDashboard() {
	tui.mutex.RLock()
	changed := tui.dashChanged
	tui.mutex.RUnlock()

	if !changed {
		return
	}

	dashboardHeight := tui.height - 4

	// Dynamic dashboard width: use remaining space after globe
	dashboardWidth := tui.width - tui.globe.Width - 3 // 3 for separator and padding
	if dashboardWidth < 50 {
		dashboardWidth = 50
	}
	// No maximum limit - use all available space

	dashLines := tui.dashboard.Render(dashboardHeight, dashboardWidth)
	separatorX := tui.globe.Width + 1
	startX := separatorX + 2

	for y := 0; y < dashboardHeight; y++ {
		tui.screen.SetContent(separatorX, y, ' ', nil, tcell.StyleDefault)
		for x := 0; x < dashboardWidth && startX+x < tui.width; x++ {
			tui.screen.SetContent(startX+x, y, ' ', nil, tcell.StyleDefault)
		}
	}

	for y := 0; y < tui.height; y++ {
		tui.screen.SetContent(separatorX, y, '|', nil,
			tcell.StyleDefault.Foreground(currentTheme.Separator))
	}

	headerStyle := tcell.StyleDefault.Foreground(currentTheme.Dashboard).Bold(true)
	connectionStyle := tcell.StyleDefault.Foreground(currentTheme.Stats)
	statusOkStyle := tcell.StyleDefault.Foreground(currentTheme.StatusOk).Bold(true)
	statusErrorStyle := tcell.StyleDefault.Foreground(currentTheme.StatusError).Bold(true)

	// Get scroll offset
	tui.state.mutex.RLock()
	scrollOffset := tui.state.dashboardScroll
	tui.state.mutex.RUnlock()

	for y, line := range dashLines {
		if y >= dashboardHeight {
			break
		}

		// Apply horizontal scroll - slice the line based on scroll offset
		lineRunes := []rune(line)
		visibleLine := ""
		scrollIndicatorLeft := ""
		scrollIndicatorRight := ""

		if len(lineRunes) > 0 {
			// Add left scroll indicator if scrolled right
			if scrollOffset > 0 {
				scrollIndicatorLeft = "◀"
			}

			// Calculate visible portion
			startIdx := scrollOffset
			endIdx := scrollOffset + dashboardWidth - 2 // -2 for scroll indicators

			if startIdx >= len(lineRunes) {
				startIdx = len(lineRunes) - 1
				if startIdx < 0 {
					startIdx = 0
				}
			}
			if endIdx > len(lineRunes) {
				endIdx = len(lineRunes)
			}
			if startIdx < len(lineRunes) {
				visibleLine = string(lineRunes[startIdx:endIdx])
			}

			// Add right scroll indicator if there's more content
			if endIdx < len(lineRunes) {
				scrollIndicatorRight = "▶"
			}
		}

		line = scrollIndicatorLeft + visibleLine + scrollIndicatorRight

		style := connectionStyle
		if y <= 1 {
			style = headerStyle
		}

		if startX < tui.width {
			if y == 0 {
				tui.drawText(startX, y, line, style)

				hpfeedsPos := strings.Index(line, "[")
				if hpfeedsPos != -1 {
					statusChar := line[hpfeedsPos+1]
					statusStyle := statusErrorStyle
					if statusChar == '+' {
						statusStyle = statusOkStyle
					}
					tui.screen.SetContent(startX+hpfeedsPos+1, y, rune(statusChar), nil, statusStyle)
				}

				geoipPos := strings.LastIndex(line, "[")
				if geoipPos != -1 && geoipPos != hpfeedsPos {
					statusChar := line[geoipPos+1]
					statusStyle := statusErrorStyle
					if statusChar == '+' {
						statusStyle = statusOkStyle
					}
					tui.screen.SetContent(startX+geoipPos+1, y, rune(statusChar), nil, statusStyle)
				}
			} else {
				tui.drawText(startX, y, line, style)
			}
		}
	}

	headerY := dashboardHeight
	if headerY < tui.height {
		// Clear the line first
		blankStyle := tcell.StyleDefault.Background(currentTheme.Background)
		for x := startX; x < startX+dashboardWidth && x < tui.width; x++ {
			tui.screen.SetContent(x, headerY, ' ', nil, blankStyle)
		}

		headerText := "[ HOURLY ATTACK STATS ]"
		headerStyle := tcell.StyleDefault.Foreground(currentTheme.Dashboard).Bold(true)
		if len(headerText) <= dashboardWidth {
			padding := (dashboardWidth - len(headerText)) / 2
			headerX := startX + padding
			tui.drawText(headerX, headerY, headerText, headerStyle)
		}
	}

	tui.mutex.Lock()
	tui.dashChanged = false
	tui.mutex.Unlock()
}

func (tui *TUI) renderStats() {
	tui.mutex.RLock()
	changed := tui.statsChanged
	tui.mutex.RUnlock()

	if !changed {
		return
	}

	// Render sparkline first
	sparkline := tui.stats.RenderSparkline()
	if len(sparkline) > 0 {
		sparkY := tui.height - 4
		sparkX := tui.width - len(sparkline) - 7
		if sparkX > 0 && sparkY > 0 {
			sparkStyle := tcell.StyleDefault.Foreground(currentTheme.Stats)
			tui.drawText(sparkX, sparkY, sparkline, sparkStyle)
		}
	}

	statsLines := tui.stats.RenderBarGraph(24)
	if len(statsLines) == 0 || len(statsLines[0]) == 0 {
		return
	}

	chartWidth := len(statsLines[0])
	startX := tui.width - chartWidth - 7
	if startX < 0 {
		startX = 0
	}

	statsStartY := tui.height - 3

	clearStyle := tcell.StyleDefault.Background(currentTheme.Background).Foreground(currentTheme.Stats)
	for y := statsStartY; y < statsStartY+3 && y < tui.height; y++ {
		for x := startX; x < startX+chartWidth && x < tui.width; x++ {
			tui.screen.SetContent(x, y, ' ', nil, clearStyle)
		}
	}

	textStyle := tcell.StyleDefault.Background(currentTheme.Background).Foreground(currentTheme.Stats)

	for i, line := range statsLines {
		y := statsStartY + i
		if y >= tui.height {
			break
		}
		tui.drawText(startX, y, line, textStyle)
	}

	tui.mutex.Lock()
	tui.statsChanged = false
	tui.mutex.Unlock()
}

func (tui *TUI) renderInfoPanel() {
	if !tui.state.showInfo {
		return
	}

	// Get most recent connection
	var conn *Connection
	if tui.dashboard != nil {
		tui.dashboard.mutex.RLock()
		if len(tui.dashboard.Connections) > 0 {
			conn = &tui.dashboard.Connections[len(tui.dashboard.Connections)-1]
		}
		tui.dashboard.mutex.RUnlock()
	}

	if conn == nil {
		return
	}

	infoText := []string{
		"╔═══════════════ ATTACK DETAILS ═══════════════╗",
		fmt.Sprintf("║ IP:         %-32s ║", conn.IP),
		fmt.Sprintf("║ City:       %-32s ║", truncateString(conn.City, 32)),
		fmt.Sprintf("║ Country:    %-32s ║", truncateString(conn.Country, 32)),
		fmt.Sprintf("║ ASN:        %-32s ║", truncateString(conn.ASN, 32)),
		fmt.Sprintf("║ Org:        %-32s ║", truncateString(conn.Org, 32)),
		fmt.Sprintf("║ rDNS:       %-32s ║", truncateString(conn.RDNS, 32)),
		fmt.Sprintf("║ Protocol:   %-32s ║", truncateString(conn.Protocol, 32)),
		fmt.Sprintf("║ User:Pass:  %-32s ║", truncateString(conn.Username+":"+conn.Password, 32)),
		fmt.Sprintf("║ Time:       %-32s ║", conn.Time.Format("2006-01-02 15:04:05")),
		"╠═══════════════════════════════════════════════╣",
		"║ Press I to close                              ║",
		"╚═══════════════════════════════════════════════╝",
	}

	startY := (tui.height - len(infoText)) / 2
	startX := (tui.width - len(infoText[0])) / 2

	panelStyle := tcell.StyleDefault.Foreground(currentTheme.Attack).Background(currentTheme.Background).Bold(true)

	for i, line := range infoText {
		y := startY + i
		if y >= 0 && y < tui.height {
			tui.drawText(startX, y, line, panelStyle)
		}
	}
}

func truncateString(s string, maxLen int) string {
	if s == "" {
		return "N/A"
	}
	if len(s) > maxLen {
		return s[:maxLen-3] + "..."
	}
	return s
}

func (tui *TUI) renderStatsPanel() {
	if !tui.state.showStats {
		return
	}

	// Aggregate stats from dashboard connections
	countryCount := make(map[string]int)
	asnCount := make(map[string]int)

	if tui.dashboard != nil {
		tui.dashboard.mutex.RLock()
		for _, conn := range tui.dashboard.Connections {
			if conn.Country != "" {
				countryCount[conn.Country]++
			}
			if conn.ASN != "" {
				asnCount[conn.ASN]++
			}
		}
		tui.dashboard.mutex.RUnlock()
	}

	// Get top 5 countries
	type statEntry struct {
		name  string
		count int
	}
	var topCountries []statEntry
	for country, count := range countryCount {
		topCountries = append(topCountries, statEntry{country, count})
	}
	// Simple bubble sort for top 5
	for i := 0; i < len(topCountries); i++ {
		for j := i + 1; j < len(topCountries); j++ {
			if topCountries[j].count > topCountries[i].count {
				topCountries[i], topCountries[j] = topCountries[j], topCountries[i]
			}
		}
	}
	if len(topCountries) > 5 {
		topCountries = topCountries[:5]
	}

	// Get top 5 ASNs
	var topASNs []statEntry
	for asn, count := range asnCount {
		topASNs = append(topASNs, statEntry{asn, count})
	}
	for i := 0; i < len(topASNs); i++ {
		for j := i + 1; j < len(topASNs); j++ {
			if topASNs[j].count > topASNs[i].count {
				topASNs[i], topASNs[j] = topASNs[j], topASNs[i]
			}
		}
	}
	if len(topASNs) > 5 {
		topASNs = topASNs[:5]
	}

	statsText := []string{
		"╔═══════ TOP ATTACKERS ═══════╗",
		"║ TOP COUNTRIES               ║",
	}

	for i, entry := range topCountries {
		line := fmt.Sprintf("║ %d. %-18s %4d ║", i+1, truncateString(entry.name, 18), entry.count)
		statsText = append(statsText, line)
	}

	statsText = append(statsText, "║                             ║")
	statsText = append(statsText, "║ TOP ASNs                    ║")

	for i, entry := range topASNs {
		line := fmt.Sprintf("║ %d. %-18s %4d ║", i+1, truncateString(entry.name, 18), entry.count)
		statsText = append(statsText, line)
	}

	statsText = append(statsText, "╠═════════════════════════════╣")
	statsText = append(statsText, "║ Press S to close            ║")
	statsText = append(statsText, "╚═════════════════════════════╝")

	startY := 2
	startX := tui.width - 33

	statsStyle := tcell.StyleDefault.Foreground(currentTheme.Stats).Background(currentTheme.Background)

	for i, line := range statsText {
		y := startY + i
		if y >= 0 && y < tui.height && startX >= 0 {
			tui.drawText(startX, y, line, statsStyle)
		}
	}
}

func (tui *TUI) renderTopIPsPanel() {
	if !tui.state.showTopIPs {
		return
	}

	// Aggregate IP addresses from dashboard connections
	ipCount := make(map[string]int)
	ipDetails := make(map[string]*Connection)

	if tui.dashboard != nil {
		tui.dashboard.mutex.RLock()
		for _, conn := range tui.dashboard.Connections {
			if conn.IP != "" {
				ipCount[conn.IP]++
				if _, exists := ipDetails[conn.IP]; !exists {
					connCopy := conn
					ipDetails[conn.IP] = &connCopy
				}
			}
		}
		tui.dashboard.mutex.RUnlock()
	}

	// Sort by count
	type ipEntry struct {
		ip    string
		count int
		conn  *Connection
	}
	var entries []ipEntry
	for ip, count := range ipCount {
		entries = append(entries, ipEntry{ip: ip, count: count, conn: ipDetails[ip]})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].count > entries[j].count
	})

	// Take top 10
	if len(entries) > 10 {
		entries = entries[:10]
	}

	// Build panel
	ipsText := []string{
		"╔═══════════════════════════════════════════════╗",
		"║          TOP ATTACKING IP ADDRESSES          ║",
		"╠═══════════════════════════════════════════════╣",
	}

	for i, entry := range entries {
		org := "Unknown"
		if entry.conn != nil && entry.conn.Org != "" {
			org = truncateString(entry.conn.Org, 20)
		}
		line := fmt.Sprintf("║ %2d. %-15s x%-4d %-20s ║", i+1, entry.ip, entry.count, org)
		ipsText = append(ipsText, line)
	}

	// Padding
	for len(ipsText) < 15 {
		ipsText = append(ipsText, "║                                               ║")
	}

	ipsText = append(ipsText, "╠═══════════════════════════════════════════════╣")
	ipsText = append(ipsText, "║ Press P to close                              ║")
	ipsText = append(ipsText, "╚═══════════════════════════════════════════════╝")

	startY := (tui.height - len(ipsText)) / 2
	startX := (tui.width - len(ipsText[0])) / 2

	panelStyle := tcell.StyleDefault.Foreground(currentTheme.Attack).Background(currentTheme.Background).Bold(true)

	for i, line := range ipsText {
		y := startY + i
		if y >= 0 && y < tui.height {
			tui.drawText(startX, y, line, panelStyle)
		}
	}
}

func (tui *TUI) renderHelpPanel() {
	if !tui.state.showHelp {
		return
	}

	helpText := []string{
		"╔═══════════════════════════════════════╗",
		"║         KEYBOARD CONTROLS             ║",
		"╠═══════════════════════════════════════╣",
		"║ Space   - Pause/Resume rotation       ║",
		"║ [/]     - Decrease/Increase spin      ║",
		"║ +/-     - Zoom in/out                 ║",
		"║ Arrows  - Nudge view angle            ║",
		"║ T       - Cycle themes                ║",
		"║ G       - Toggle attack arcs          ║",
		"║ L       - Toggle lighting             ║",
		"║ R       - Toggle Matrix rain          ║",
		"║ I       - Toggle attack info panel    ║",
		"║ S       - Toggle stats panel          ║",
		"║ P       - Toggle top IPs panel        ║",
		"║ , / .   - Scroll dashboard left/right ║",
		"║ H       - Reset dashboard scroll      ║",
		"║ C       - Toggle command guide        ║",
		"║ ?       - Toggle this help panel      ║",
		"║ Q/X/Esc - Exit                        ║",
		"╚═══════════════════════════════════════╝",
	}

	startY := (tui.height - len(helpText)) / 2
	startX := (tui.width - len(helpText[0])) / 2

	helpStyle := tcell.StyleDefault.Foreground(currentTheme.Dashboard).Background(currentTheme.Background)

	for i, line := range helpText {
		y := startY + i
		if y >= 0 && y < tui.height {
			tui.drawText(startX, y, line, helpStyle)
		}
	}
}

func (tui *TUI) renderCommandGuide() {
	y := tui.height - 1
	if y < 0 || y >= tui.height {
		return
	}

	// Always clear the bottom line first
	blankStyle := tcell.StyleDefault.Background(currentTheme.Background)
	for x := 0; x < tui.width; x++ {
		tui.screen.SetContent(x, y, ' ', nil, blankStyle)
	}

	if !tui.state.showCommands {
		return
	}

	// Command guide at bottom of screen
	guideLines := []string{
		"T:Theme L:Light G:Arcs R:Rain I:Info S:Stats P:TopIPs ,:Left .:Right H:Home Space:Pause []:Speed +-:Zoom Arrows:Nudge C:Guide ?:Help Q:Quit",
	}

	guideStyle := tcell.StyleDefault.Foreground(currentTheme.Dashboard).Background(currentTheme.Background).Bold(true)

	// Center the guide text
	text := guideLines[0]
	if len(text) > tui.width {
		text = text[:tui.width]
	}
	startX := (tui.width - len(text)) / 2
	if startX < 0 {
		startX = 0
	}
	tui.drawText(startX, y, text, guideStyle)
}

func (tui *TUI) Render(rotation float64, protocolGlyphs bool) {
	tui.renderGlobe(rotation, protocolGlyphs)
	tui.renderDashboard()
	tui.renderStats()
	tui.renderInfoPanel()
	tui.renderStatsPanel()
	tui.renderTopIPsPanel()
	tui.renderCommandGuide()
	tui.renderHelpPanel()
	tui.screen.Show()

	// Record frame if recording enabled
	if tui.recorder != nil && tui.recorder.enabled {
		// Extract screen content
		screen := make([][]rune, tui.height)
		for y := 0; y < tui.height; y++ {
			screen[y] = make([]rune, tui.width)
			for x := 0; x < tui.width; x++ {
				mainc, _, _, _ := tui.screen.GetContent(x, y)
				screen[y][x] = mainc
			}
		}
		tui.recorder.RecordFrame(screen)
	}
}

func (tui *TUI) pollEvents(aspectRatio float64) chan bool {
	quit := make(chan bool, 1)
	go func() {
		for {
			ev := tui.screen.PollEvent()
			switch ev := ev.(type) {
			case *tcell.EventKey:
				switch ev.Key() {
				case tcell.KeyCtrlC:
					quit <- true
					return
				case tcell.KeyEscape:
					quit <- true
					return
				case tcell.KeyRune:
					r := ev.Rune()
					switch r {
					case 'q', 'Q', 'x', 'X':
						quit <- true
						return
					case ' ':
						tui.state.mutex.Lock()
						tui.state.paused = !tui.state.paused
						tui.state.mutex.Unlock()
					case '[':
						tui.state.mutex.Lock()
						tui.state.spinSpeed = math.Max(0.1, tui.state.spinSpeed-0.1)
						tui.state.mutex.Unlock()
					case ']':
						tui.state.mutex.Lock()
						tui.state.spinSpeed = math.Min(5.0, tui.state.spinSpeed+0.1)
						tui.state.mutex.Unlock()
					case '+', '=':
						tui.globe.Zoom = math.Min(3.0, tui.globe.Zoom+0.1)
						tui.MarkGlobeChanged()
					case '-', '_':
						tui.globe.Zoom = math.Max(0.5, tui.globe.Zoom-0.1)
						tui.MarkGlobeChanged()
					case 't', 'T':
						// Cycle themes
						themeNames := []string{"default", "matrix", "amber", "solarized", "nord", "dracula", "mono", "rainbow", "skittles"}
						tui.state.mutex.Lock()
						tui.state.currentTheme = (tui.state.currentTheme + 1) % len(themeNames)
						currentTheme = themes[themeNames[tui.state.currentTheme]]
						tui.state.mutex.Unlock()
						tui.MarkGlobeChanged()
						tui.MarkDashboardChanged()
						tui.MarkStatsChanged()
					case 'c', 'C':
						tui.state.mutex.Lock()
						tui.state.showCommands = !tui.state.showCommands
						tui.state.mutex.Unlock()
						tui.MarkGlobeChanged()
					case 'g', 'G':
						tui.state.mutex.Lock()
						tui.state.showArcs = !tui.state.showArcs
						tui.state.mutex.Unlock()
						if globalArcManager != nil {
							globalArcManager.mutex.Lock()
							if tui.state.showArcs {
								// Restore saved style or default to curved
								if tui.state.savedArcStyle == "" || tui.state.savedArcStyle == "off" {
									globalArcManager.arcStyle = "curved"
									tui.state.savedArcStyle = "curved"
								} else {
									globalArcManager.arcStyle = tui.state.savedArcStyle
								}
							} else {
								// Save current style and turn off
								tui.state.savedArcStyle = globalArcManager.arcStyle
								globalArcManager.arcStyle = "off"
							}
							globalArcManager.mutex.Unlock()
						}
					case 'l', 'L':
						tui.globe.Lighting = !tui.globe.Lighting
						tui.MarkGlobeChanged()
					case 'r', 'R':
						if tui.rain != nil {
							tui.rain.SetEnabled(!tui.rain.enabled)
							tui.MarkGlobeChanged()
						}
					case '?':
						tui.state.mutex.Lock()
						tui.state.showHelp = !tui.state.showHelp
						tui.state.mutex.Unlock()
						tui.MarkGlobeChanged()
					case 'i', 'I':
						tui.state.mutex.Lock()
						tui.state.showInfo = !tui.state.showInfo
						tui.state.mutex.Unlock()
						tui.MarkGlobeChanged()
					case 's', 'S':
						tui.state.mutex.Lock()
						tui.state.showStats = !tui.state.showStats
						tui.state.mutex.Unlock()
						tui.MarkGlobeChanged()
						tui.MarkDashboardChanged()
						tui.MarkStatsChanged()
					case 'p', 'P':
						tui.state.mutex.Lock()
						tui.state.showTopIPs = !tui.state.showTopIPs
						tui.state.mutex.Unlock()
						tui.MarkGlobeChanged()
						tui.MarkDashboardChanged()
					case ',', '<':
						// Scroll dashboard left
						tui.state.mutex.Lock()
						tui.state.dashboardScroll -= 5
						if tui.state.dashboardScroll < 0 {
							tui.state.dashboardScroll = 0
						}
						tui.state.mutex.Unlock()
						tui.MarkDashboardChanged()
					case '.', '>':
						// Scroll dashboard right
						tui.state.mutex.Lock()
						tui.state.dashboardScroll += 5
						tui.state.mutex.Unlock()
						tui.MarkDashboardChanged()
					case 'h', 'H':
						// Reset scroll to home position
						tui.state.mutex.Lock()
						tui.state.dashboardScroll = 0
						tui.state.mutex.Unlock()
						tui.MarkDashboardChanged()
					}
				case tcell.KeyUp:
					tui.globe.NudgeY -= 2
					tui.MarkGlobeChanged()
				case tcell.KeyDown:
					tui.globe.NudgeY += 2
					tui.MarkGlobeChanged()
				case tcell.KeyLeft:
					tui.globe.NudgeX -= 2
					tui.MarkGlobeChanged()
				case tcell.KeyRight:
					tui.globe.NudgeX += 2
					tui.MarkGlobeChanged()
				}
			case *tcell.EventResize:
				tui.HandleResize(aspectRatio)
			}
		}
	}()
	return quit
}

func getEarthBitmap() []string {
	return []string{
		"                                                                                                                        ",
		"                                                                                                                        ",
		"                                                                                                                        ",
		"                             # ####### #################                                    #                           ",
		"                       #    #   ### #################            ###                                                    ",
		"                      ###  ## ####       ############ #                        ##         ########        #####         ",
		"                  ## ###   #  ### ##      ###########                         #    #### ################   ###          ",
		"      ######## ###### #### # #  #  ###     #########              #######        # ## ##################################",
		" ### ###########################    ####   #####      #          ####### ###############################################",
		"      ########################       ##    ####                #### ####################################################",
		"      ### # #################      ##        #                ##### # ##########################################  ##    ",
		"                ##############     #####                   #     #  #######################################      ##     ",
		"                 ################ #######                # #   ###########################################      ##      ",
		"                  ########################                 ################################################             ",
		"                    ###################  ##                ################################################             ",
		"                   ################### #                    ##########  ####  ############################              ",
		"                   ##################                    ##### ##  ###    ### ##########################                ",
		"                   #################                     ###       # ######## ######################  #    #            ",
		"                    ###############                       #  ###       ##############################  #  #             ",
		"                     #############                        ######        #############################                   ",
		"                       ######## #                        ############################################                   ",
		"                      # ####     #                      ##################### #######################                   ",
		"                       # ###      #                    ################# ######    #################                    ",
		"                         ###  #   #                    ################## ######     ####  #####                        ",
		"                          #####   # #                  ################## #####      ###    ####                        ",
		"                             ####                      ################### ###       ##      ####   #                   ",
		"                               #    #                  ####################           #      # ##                       ",
		"                                #  #####                #####################         #      # #     ##                 ",
		"                                   ######                #### ###############          #      #    #                    ",
		"                                   ########                     ############                 ##   ##                    ",
		"                                  #########                     ###########                   #  ####                   ",
		"                                  #############                 ##########                    ##### #     ##            ",
		"                                 ################                ########                                  ## #         ",
		"                                  ###############                #########                         ## #    # #          ",
		"                                   #############                 #########                                              ",
		"                                   ############                  #########  #                         # ##  #           ",
		"                                     ##########                 #########  ##                        ########           ",
		"                                     ##########                  #######   ##                      ###########     #    ",
		"                                     ########                    #######   #                      #############         ",
		"                                     #######                     ######                           ##############        ",
		"                                     #######                      #####                            #############        ",
		"                                     ######                       ####                             ###   ######         ",
		"                                    #####                                                                  ####       # ",
		"                                    #####                                                                              #",
		"                                    ###                                                                      #        # ",
		"                                    ###                                                                             ##  ",
		"                                    ##                                                                                  ",
		"                                   ##                                                                                   ",
		"                                    ##                                                                                  ",
		"                                                                                                                        ",
		"                                                                                                                        ",
		"                                                                                                                        ",
		"                                       #                                                                                ",
		"                                      #                                #  ##########   ########################         ",
		"                                   #####                 ########################## #################################   ",
		"                  # ## #   #############              #############################################################     ",
		"        ## #########################             ##################################################################     ",
		"           ######################## #  #  ##     #################################################################      ",
		"    ##################################################################################################################  ",
		"########################################################################################################################",
	}
}

func showHelp() {
	fmt.Printf(`SecKC-MHN-Globe Enhanced - TUI Earth visualization with honeypot monitoring

DESCRIPTION:
    Terminal-based application displaying a rotating 3D ASCII globe with a live
    dashboard of incoming connection attempts. NOW WITH ENHANCED FEATURES!

USAGE:
    SecKC-MHN-Globe-Enhanced [OPTIONS]

OPTIONS:
    -h                Show this help message
    -d <filename>     Enable debug logging to specified file
    -s <seconds>      Globe rotation period in seconds (10-300, default: 30)
    -r <milliseconds> Globe refresh rate in milliseconds (50-1000, default: 100)
    -m                Enable monochrome mode
    -a <ratio>        Character aspect ratio (height/width, 1.0-4.0, default: 2.0)
    -u <url>          Base URL for SecKC API
    -e <count>        Maximum events to fetch per API call (1-500, default: 50)
    -p <duration>     API polling interval (1s-300s, default: 2s)

ENHANCED OPTIONS:
    --charset <type>      Character set: ascii|blocks|braille (default: ascii)
    --theme <name>        Theme: default|matrix|amber|solarized|nord|dracula|mono
    --arcs <style>        Attack arcs: curved|straight|off (default: off)
    --trail-ms <ms>       Arc trail persistence in milliseconds (default: 1200)
    --lighting            Enable globe lighting/shading
    --light-lon <deg>     Light source longitude (-180 to 180)
    --light-lat <deg>     Light source latitude (-90 to 90)
    --light-follow        Light rotates opposite to globe
    --crt                 Enable CRT scanline effect
    --glow <level>        Phosphor glow level 0-3 (default: 0)
    --rain                Enable Matrix rain effect
    --rain-density <n>    Rain density 0-10 (default: 5)
    --protocol-glyphs     Show protocol-specific glyphs instead of asterisks
    --demo-storm          Enable demo storm generator
    --demo-rate <n>       Demo attack rate per second (default: 10)
    --record <file>       Record session to asciinema file
    --config <file>       Load settings from TOML config file

INTERACTIVE CONTROLS:
    Space    - Pause/Resume rotation
    [/]      - Decrease/Increase spin speed
    +/-      - Zoom in/out
    Arrows   - Nudge view angle
    T        - Cycle through themes
    C        - Toggle coastlines/grid
    G        - Toggle great-circle arcs
    L        - Toggle lighting
    R        - Toggle Matrix rain
    ?        - Toggle help panel
    Q/X/Esc  - Exit

EXAMPLES:
    # Default enhanced mode
    ./SecKC-MHN-Globe-Enhanced

    # Matrix theme with rain and Braille characters
    ./SecKC-MHN-Globe-Enhanced --theme matrix --rain --charset braille

    # Attack arcs with lighting
    ./SecKC-MHN-Globe-Enhanced --arcs curved --lighting --light-follow

    # Demo mode with recording
    ./SecKC-MHN-Globe-Enhanced --demo-storm --demo-rate 50 --record demo.cast

    # Full experience
    ./SecKC-MHN-Globe-Enhanced --theme matrix --charset braille --arcs curved --lighting --light-follow --rain --protocol-glyphs --crt

`)
}

func main() {
	// Basic flags
	var debugFile = flag.String("d", "", "Debug log filename")
	var showHelpFlag = flag.Bool("h", false, "Show help")
	var rotationPeriod = flag.Int("s", 30, "Globe rotation period in seconds")
	var refreshRate = flag.Int("r", 100, "Globe refresh rate in milliseconds")
	var monochrome = flag.Bool("m", false, "Enable monochrome mode")
	var aspectRatio = flag.Float64("a", 2.0, "Character aspect ratio")
	var baseURL = flag.String("u", "https://mhn.h-i-r.net/seckcapi", "Base URL for SecKC API")
	var maxEvents = flag.Int("e", 50, "Maximum events to fetch per API call")
	var pollInterval = flag.Duration("p", 2*time.Second, "API polling interval")

	// Enhanced flags
	var charset = flag.String("charset", "ascii", "Character set: ascii|blocks|braille")
	var themeName = flag.String("theme", "default", "Theme name")
	var arcStyle = flag.String("arcs", "off", "Attack arcs: curved|straight|off")
	var trailMS = flag.Int("trail-ms", 1200, "Arc trail persistence in milliseconds")
	var lighting = flag.Bool("lighting", false, "Enable globe lighting/shading")
	var lightLon = flag.Float64("light-lon", 0, "Light source longitude")
	var lightLat = flag.Float64("light-lat", 0, "Light source latitude")
	var lightFollow = flag.Bool("light-follow", false, "Light follows rotation")
	var crtEffect = flag.Bool("crt", false, "Enable CRT scanline effect")
	var glowLevel = flag.Int("glow", 0, "Phosphor glow level 0-3")
	var rainEffect = flag.Bool("rain", false, "Enable Matrix rain effect")
	var rainDensity = flag.Int("rain-density", 5, "Rain density 0-10")
	var protocolGlyphs = flag.Bool("protocol-glyphs", false, "Show protocol glyphs")
	var demoStorm = flag.Bool("demo-storm", false, "Enable demo storm generator")
	var demoRate = flag.Int("demo-rate", 10, "Demo attack rate per second")
	var recordFile = flag.String("record", "", "Record to asciinema file")
	var configFile = flag.String("config", "", "Load from TOML config file")

	flag.Parse()

	if *showHelpFlag {
		showHelp()
		os.Exit(0)
	}

	// Load config file if specified
	var config *Config
	var err error
	if *configFile != "" {
		config, err = LoadConfig(*configFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
			os.Exit(1)
		}

		// Apply config file settings (flags override config file)
		if config.API.BaseURL != "" && *baseURL == "https://mhn.h-i-r.net/seckcapi" {
			*baseURL = config.API.BaseURL
		}
		if config.Display.Theme != "" && *themeName == "default" {
			*themeName = config.Display.Theme
		}
		if config.Display.Charset != "" && *charset == "ascii" {
			*charset = config.Display.Charset
		}
	}

	// Validate parameters
	if *rotationPeriod < 10 || *rotationPeriod > 300 {
		fmt.Fprintf(os.Stderr, "Error: Rotation period must be between 10 and 300 seconds\n")
		os.Exit(1)
	}

	if *refreshRate < 50 || *refreshRate > 1000 {
		fmt.Fprintf(os.Stderr, "Error: Refresh rate must be between 50 and 1000 milliseconds\n")
		os.Exit(1)
	}

	if *aspectRatio < 1.0 || *aspectRatio > 4.0 {
		fmt.Fprintf(os.Stderr, "Error: Aspect ratio must be between 1.0 and 4.0\n")
		os.Exit(1)
	}

	// Debug logging
	if *debugFile != "" {
		file, err := os.OpenFile(*debugFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error opening debug log: %v\n", err)
			os.Exit(1)
		}
		defer file.Close()
		debugLogger = log.New(file, "", log.LstdFlags|log.Lmicroseconds)
		debugLog("SecKC-MHN-Globe Enhanced starting")
	}

	// Initialize theme
	if *monochrome {
		*themeName = "mono"
	}
	if theme, exists := themes[*themeName]; exists {
		currentTheme = theme
	} else {
		currentTheme = themes["default"]
	}
	debugLog("Theme: %s", currentTheme.Name)

	// Parse charset
	var charsetType Charset
	switch *charset {
	case "braille":
		charsetType = CharsetBraille
	case "blocks":
		charsetType = CharsetBlocks
	default:
		charsetType = CharsetASCII
	}
	debugLog("Charset: %s", *charset)

	rand.Seed(time.Now().UnixNano())

	// Initialize API
	apiConfig := createAPIConfig(*baseURL, *pollInterval, *maxEvents)
	apiClient := NewAPIClient(apiConfig)

	// Initialize GeoIP
	geoIPManager := NewGeoIPManager(apiClient)
	globalGeoIP = geoIPManager
	globalGeoIPAvailable = true

	// Initialize Arc Manager
	globalArcManager = NewArcManager(*arcStyle, *trailMS)

	// Initialize Demo Storm
	globalDemoStorm = NewDemoStorm()
	if *demoStorm {
		globalDemoStorm.enabled = true
		globalDemoStorm.rate = *demoRate
	}

	// Initialize TUI
	tui, err := NewTUI(*aspectRatio, charsetType, *recordFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error initializing TUI: %v\n", err)
		os.Exit(1)
	}
	defer tui.Close()

	globalTUI = tui

	// Configure globe lighting
	if *lighting {
		tui.globe.Lighting = true
		tui.globe.LightLon = *lightLon
		tui.globe.LightLat = *lightLat
		tui.globe.LightFollow = *lightFollow
	}

	// Configure CRT effect
	if *crtEffect {
		tui.crt.enabled = true
		tui.crt.glowLevel = *glowLevel
	}

	// Configure Matrix rain
	if *rainEffect {
		tui.rain.SetEnabled(true)
		tui.rain.density = *rainDensity
	}

	quit := tui.pollEvents(*aspectRatio)

	sharedDashboard := NewDashboard(tui.height - 4)
	tui.dashboard = sharedDashboard

	// Start API client
	err = startAPIClient(apiClient, sharedDashboard)
	useLiveData := false
	if err == nil {
		globalAPIConnected = true
		useLiveData = true
	}

	// Start demo storm if enabled
	if globalDemoStorm.enabled {
		globalDemoStorm.Start(sharedDashboard)
		useLiveData = true // Don't generate random data if demo storm is active
	}

	startTime := time.Now()
	lastConnectionTime := time.Now()
	lastGlobeUpdate := time.Now()
	lastStatsUpdate := time.Now()
	lastArcCleanup := time.Now()
	lastRainUpdate := time.Now()
	lastCRTUpdate := time.Now()

	nextMockInterval := time.Duration(200+rand.Intn(4800)) * time.Millisecond

	// Fetch initial stats
	go func() {
		if err := tui.stats.FetchData(); err != nil {
			debugLog("Stats: Initial fetch failed: %v", err)
		} else {
			tui.MarkStatsChanged()
		}
	}()

	// Main loop
	for {
		select {
		case <-quit:
			debugLog("Shutting down")
			if globalDemoStorm != nil {
				globalDemoStorm.Stop()
			}
			tui.Close()
			fmt.Println("Exiting...")
			os.Exit(0)
		default:
		}

		now := time.Now()

		// Update globe rotation
		if now.Sub(lastGlobeUpdate) >= time.Duration(*refreshRate)*time.Millisecond {
			tui.MarkGlobeChanged()
			lastGlobeUpdate = now
		}

		// Generate mock data if needed
		if !useLiveData && now.Sub(lastConnectionTime) >= nextMockInterval {
			tui.dashboard.GenerateRandomConnection()
			lastConnectionTime = now
			nextMockInterval = time.Duration(200+rand.Intn(4800)) * time.Millisecond
		}

		// Update stats
		if now.Sub(lastStatsUpdate) >= 300*time.Second {
			go func() {
				if err := tui.stats.FetchData(); err != nil {
					debugLog("Stats: Fetch failed: %v", err)
				} else {
					tui.MarkStatsChanged()
				}
			}()
			lastStatsUpdate = now
		}

		// Cleanup expired arcs
		if globalArcManager != nil && now.Sub(lastArcCleanup) >= 100*time.Millisecond {
			globalArcManager.CleanupExpired()
			lastArcCleanup = now
		}

		// Update rain effect
		if tui.rain != nil && tui.rain.enabled && now.Sub(lastRainUpdate) >= 50*time.Millisecond {
			tui.rain.Update()
			lastRainUpdate = now
			tui.MarkGlobeChanged()
		}

		// Update CRT effect
		if tui.crt != nil && tui.crt.enabled && now.Sub(lastCRTUpdate) >= 100*time.Millisecond {
			tui.crt.Update()
			lastCRTUpdate = now
		}

		// Calculate rotation with pause support
		var rotation float64
		tui.state.mutex.RLock()
		if !tui.state.paused {
			elapsed := now.Sub(startTime).Seconds()
			rotation = -(elapsed / float64(*rotationPeriod)) * 2 * math.Pi * tui.state.spinSpeed
		}
		tui.state.mutex.RUnlock()

		tui.Render(rotation, *protocolGlyphs)

		time.Sleep(50 * time.Millisecond)
	}
}
