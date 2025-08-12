package main

import (
	"bufio"
	"bytes"
	"crypto/sha1"
	"encoding/binary"
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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/oschwald/geoip2-golang"
)

type Connection struct {
	IP       string
	Username string
	Password string
	Time     time.Time
}

type HPFeedsConfig struct {
	Ident   string
	Secret  string
	Server  string
	Port    string
	Channel string
}

type HPFeedsMessage struct {
	Name    string
	Payload []byte
}

type rawMsgHeader struct {
	Length uint32
	Opcode uint8
}

const (
	opcode_err  = 0
	opcode_info = 1
	opcode_auth = 2
	opcode_pub  = 3
	opcode_sub  = 4
)

type Hpfeeds struct {
	LocalAddr    net.TCPAddr
	conn         *net.TCPConn
	host         string
	port         int
	ident        string
	auth         string
	channel      map[string]chan HPFeedsMessage
	authSent     chan bool
	Disconnected chan error
	Log          bool
}

type CowrieSession struct {
	Session         string   `json:"session"`
	StartTime       string   `json:"startTime"`
	EndTime         string   `json:"endTime"`
	PeerIP          string   `json:"peerIP"`
	PeerPort        int      `json:"peerPort"`
	HostIP          string   `json:"hostIP"`
	HostPort        int      `json:"hostPort"`
	LoggedIn        []string `json:"loggedin"`
	Credentials     []string `json:"credentials"`
	Commands        []string `json:"commands"`
	UnknownCommands []string `json:"unknownCommands"`
	URLs            []string `json:"urls"`
	Version         *string  `json:"version"`
	TTYLog          *string  `json:"ttylog"`
	Hashes          []string `json:"hashes"`
	Protocol        string   `json:"protocol"`
}

type Dashboard struct {
	Connections []Connection
	MaxLines    int
	mutex       sync.RWMutex
}

var debugLogger *log.Logger

// Global color variables for theming
var (
	colorBackground  tcell.Color
	colorText        tcell.Color
	colorGlobe       tcell.Color
	colorAttack      tcell.Color
	colorDashboard   tcell.Color
	colorStats       tcell.Color
	colorSeparator   tcell.Color
	colorStatusOk    tcell.Color
	colorStatusError tcell.Color
)

// initializeColors sets up the global color variables based on monochrome mode
func initializeColors(monochrome bool) {
	// Background is always black
	colorBackground = tcell.ColorBlack

	if monochrome {
		colorText = tcell.ColorWhite
		colorGlobe = tcell.ColorWhite
		colorAttack = tcell.ColorWhite
		colorDashboard = tcell.ColorWhite
		colorStats = tcell.ColorWhite
		colorSeparator = tcell.ColorWhite
		colorStatusOk = tcell.ColorWhite
		colorStatusError = tcell.ColorWhite
	} else {
		colorText = tcell.ColorWhite
		colorGlobe = tcell.ColorGreen
		colorAttack = tcell.ColorRed
		colorDashboard = tcell.ColorYellow
		colorStats = tcell.ColorAqua
		colorSeparator = tcell.ColorGray
		colorStatusOk = tcell.ColorGreen
		colorStatusError = tcell.ColorRed
	}
}

type GeoIPManager struct {
	db    *geoip2.Reader
	mutex sync.RWMutex
}

type LocationInfo struct {
	City      string
	Country   string
	Latitude  float64
	Longitude float64
	Valid     bool
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

func NewGeoIPManager() *GeoIPManager {
	return &GeoIPManager{}
}

func (g *GeoIPManager) LoadDatabase(dbPath string) error {
	g.mutex.Lock()
	defer g.mutex.Unlock()

	if g.db != nil {
		g.db.Close()
	}

	db, err := geoip2.Open(dbPath)
	if err != nil {
		return err
	}

	g.db = db
	debugLog("GeoIP: Database loaded from %s", dbPath)
	return nil
}

func (g *GeoIPManager) Close() {
	g.mutex.Lock()
	defer g.mutex.Unlock()

	if g.db != nil {
		g.db.Close()
		g.db = nil
	}
}

func (g *GeoIPManager) LookupIP(ipStr string) LocationInfo {
	g.mutex.RLock()
	defer g.mutex.RUnlock()

	if g.db == nil {
		return LocationInfo{Valid: false}
	}

	ip := net.ParseIP(ipStr)
	if ip == nil {
		return LocationInfo{Valid: false}
	}

	record, err := g.db.City(ip)
	if err != nil {
		debugLog("GeoIP: Failed to lookup %s: %v", ipStr, err)
		return LocationInfo{Valid: false}
	}

	locationInfo := LocationInfo{
		City:      record.City.Names["en"],
		Country:   record.Country.Names["en"],
		Latitude:  record.Location.Latitude,
		Longitude: record.Location.Longitude,
		Valid:     true,
	}

	debugLog("GeoIP: %s located at %.4f,%.4f (%s, %s)",
		ipStr, locationInfo.Latitude, locationInfo.Longitude, locationInfo.City, locationInfo.Country)

	return locationInfo
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
	debugLog("Stats: Fetching %s data from URL: %s", label, url)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		debugLog("Stats: %s HTTP request failed: %v", label, err)
		return nil, err
	}
	defer resp.Body.Close()

	debugLog("Stats: %s HTTP response status: %d %s", label, resp.StatusCode, resp.Status)

	if resp.StatusCode != http.StatusOK {
		debugLog("Stats: %s Non-200 status code received: %d", label, resp.StatusCode)
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	// Read the entire response body for logging
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		debugLog("Stats: %s Failed to read response body: %v", label, err)
		return nil, err
	}

	debugLog("Stats: %s Complete API response body: %s", label, string(body))

	// Parse the JSON response
	var stats StatsResponse
	if err := json.Unmarshal(body, &stats); err != nil {
		debugLog("Stats: %s JSON parsing failed: %v", label, err)
		return nil, err
	}

	debugLog("Stats: %s Fetched data successfully, %d entries", label, len(stats))

	// Log the parsed hourly data points
	if len(stats) > 0 {
		debugLog("Stats: %s Date: %s, Channel: %s", label, stats[0].Date, stats[0].Channel)
		debugLog("Stats: %s Hourly data points:", label)
		for hour := 0; hour < 24; hour++ {
			hourStr := fmt.Sprintf("%d", hour)
			if count, exists := stats[0].Hourly[hourStr]; exists {
				debugLog("Stats: %s   Hour %02d: %d attacks", label, hour, count)
			} else {
				debugLog("Stats: %s   Hour %02d: 0 attacks (no data)", label, hour)
			}
		}

		// Calculate and log statistics
		totalAttacks := 0
		maxHour := 0
		maxCount := 0
		for hourStr, count := range stats[0].Hourly {
			totalAttacks += count
			if hour, err := strconv.Atoi(hourStr); err == nil && count > maxCount {
				maxCount = count
				maxHour = hour
			}
		}
		debugLog("Stats: %s Total attacks: %d", label, totalAttacks)
		debugLog("Stats: %s Peak hour: %02d with %d attacks", label, maxHour, maxCount)
	} else {
		debugLog("Stats: %s No data entries received", label)
	}

	return stats, nil
}

func (s *StatsManager) FetchData() error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	// Update URLs to current/previous date every time we fetch
	s.updateURLs()

	// Only fetch if more than 5 minutes have passed
	if time.Since(s.lastFetch) < 5*time.Minute && len(s.todayData) > 0 && len(s.yesterdayData) > 0 {
		debugLog("Stats: Using cached data (last fetch: %v ago)", time.Since(s.lastFetch))
		return nil
	}

	// Fetch today's data
	todayData, err := s.fetchFromURL(s.todayURL, "Today")
	if err != nil {
		debugLog("Stats: Failed to fetch today's data: %v", err)
		// Don't return error, try yesterday's data anyway
	} else {
		s.todayData = todayData
	}

	// Fetch yesterday's data
	yesterdayData, err := s.fetchFromURL(s.yesterdayURL, "Yesterday")
	if err != nil {
		debugLog("Stats: Failed to fetch yesterday's data: %v", err)
		// Don't return error, we might have today's data
	} else {
		s.yesterdayData = yesterdayData
	}

	// Update last fetch time if we got at least one dataset
	if len(s.todayData) > 0 || len(s.yesterdayData) > 0 {
		s.lastFetch = time.Now()
		debugLog("Stats: Data fetch completed successfully")
	} else {
		debugLog("Stats: Failed to fetch any data")
		return fmt.Errorf("no data available from either day")
	}

	return nil
}

func (s *StatsManager) GetHourlyData() map[string]int {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	// Create 24-hour rolling window
	rollingData := make(map[string]int)
	currentHour := time.Now().Hour()

	debugLog("Stats: Creating 24-hour rolling window, current hour: %d", currentHour)

	// For each of the past 24 hours, get the appropriate data
	for i := 0; i < 24; i++ {
		// Calculate which hour we need (going backwards from current hour)
		targetHour := (currentHour - i + 24) % 24
		targetHourStr := fmt.Sprintf("%d", targetHour)

		var count int
		var found bool

		// If this hour is from today (i.e., i <= currentHour), use today's data
		if i <= currentHour && len(s.todayData) > 0 {
			count, found = s.todayData[0].Hourly[targetHourStr]
			if found {
				debugLog("Stats: Rolling hour %d (actual hour %02d): %d attacks (from today)", 23-i, targetHour, count)
			} else {
				debugLog("Stats: Rolling hour %d (actual hour %02d): 0 attacks (no today data)", 23-i, targetHour)
			}
		} else if len(s.yesterdayData) > 0 {
			// This hour is from yesterday
			count, found = s.yesterdayData[0].Hourly[targetHourStr]
			if found {
				debugLog("Stats: Rolling hour %d (actual hour %02d): %d attacks (from yesterday)", 23-i, targetHour, count)
			} else {
				debugLog("Stats: Rolling hour %d (actual hour %02d): 0 attacks (no yesterday data)", 23-i, targetHour)
			}
		}

		// Store with the rolling position as key (0 = oldest, 23 = newest/current hour)
		rollingKey := fmt.Sprintf("%d", 23-i)
		rollingData[rollingKey] = count
	}

	debugLog("Stats: 24-hour rolling window created with %d data points", len(rollingData))
	return rollingData
}

func (s *StatsManager) RenderBarGraph(width int) []string {
	hourlyData := s.GetHourlyData()
	debugLog("BarGraph: Received %d data points", len(hourlyData))

	if len(hourlyData) == 0 {
		debugLog("BarGraph: No data, returning empty bars")
		return []string{"", "", ""}
	}

	// Find max value for scaling
	maxVal := 0
	for key, count := range hourlyData {
		debugLog("BarGraph: Position %s = %d attacks", key, count)
		if count > maxVal {
			maxVal = count
		}
	}

	debugLog("BarGraph: Max value for scaling: %d", maxVal)
	if maxVal == 0 {
		debugLog("BarGraph: Max value is 0, returning empty bars")
		return []string{"", "", ""}
	}

	// Build 3-line compact bar graph with scale labels
	lines := make([]string, 3)

	// Determine how many hours we can fit - reserve space for scale labels
	chartWidth := 24 // Fixed chart width
	maxValStr := fmt.Sprintf("%d", maxVal)
	labelWidth := len(maxValStr) + 1 // Space for max value + space

	debugLog("BarGraph: Chart width: %d, label width: %d", chartWidth, labelWidth)

	// Build bars for each line (top to bottom)
	for lineIdx := 0; lineIdx < 3; lineIdx++ {
		var line string

		// Add scale label at the beginning of each line
		if lineIdx == 0 {
			// Top line gets the max value
			line = fmt.Sprintf("%*s ", labelWidth-1, maxValStr)
		} else if lineIdx == 2 {
			// Bottom line gets "0"
			line = fmt.Sprintf("%*s ", labelWidth-1, "0")
		} else {
			// Middle line gets spaces
			line = fmt.Sprintf("%*s ", labelWidth-1, "")
		}

		// Add the chart bars
		for pos := 0; pos < chartWidth && pos < 24; pos++ {
			// Rolling data uses positions 0-23, where 0 is oldest, 23 is newest
			posStr := fmt.Sprintf("%d", pos)
			count, exists := hourlyData[posStr]
			if !exists {
				count = 0
			}

			// Calculate normalized height (0-3 scale, where 3 is max height)
			normalizedHeight := float64(count) / float64(maxVal) * 3.0

			// Determine which character to use for this position
			// Line 0 = top, Line 1 = middle, Line 2 = bottom
			lineHeight := 3 - lineIdx // 3, 2, 1 for lines 0, 1, 2

			var barChar rune
			if normalizedHeight >= float64(lineHeight) {
				// This line should be fully filled
				barChar = '#'
			} else if normalizedHeight >= float64(lineHeight-1) {
				// Partial fill based on remainder
				remainder := normalizedHeight - float64(lineHeight-1)
				if remainder >= 0.66 {
					barChar = '#' // Full block
				} else if remainder >= 0.33 {
					barChar = '=' // Half block
				} else if remainder > 0 {
					barChar = '_' // Low block
				} else {
					barChar = ' ' // Empty
				}
			} else {
				barChar = ' '
			}

			if lineIdx == 0 && pos < 5 { // Only log first few positions on top line to avoid spam
				debugLog("BarGraph: Pos %d, Count %d, NormHeight %.2f, LineHeight %d, Char '%c'",
					pos, count, normalizedHeight, lineHeight, barChar)
			}

			line += string(barChar)
		}
		lines[lineIdx] = line
	}

	return lines
}

var globalGeoIP *GeoIPManager
var globalHPFeedsConnected bool
var globalGeoIPAvailable bool

type TUI struct {
	screen       tcell.Screen
	width        int
	height       int
	globe        *Globe
	dashboard    *Dashboard
	stats        *StatsManager
	globeChanged bool
	dashChanged  bool
	statsChanged bool
	aspectRatio  float64
	mutex        sync.RWMutex
}

type Globe struct {
	Radius      float64
	Width       int
	Height      int
	EarthMap    []string
	MapWidth    int
	MapHeight   int
	AspectRatio float64
}

func NewGlobe(width, height int, aspectRatio float64) *Globe {
	// Ensure minimum dimensions to prevent panics
	if width < 1 {
		width = 1
	}
	if height < 1 {
		height = 1
	}

	// Use provided dimensions directly (TUI now handles sizing)
	globeWidth := width
	effectiveHeight := float64(height) * aspectRatio
	radius := math.Min(float64(globeWidth)/2.5, effectiveHeight/2.5)

	// Ensure minimum radius
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
	}
}

func NewDashboard(maxLines int) *Dashboard {
	d := &Dashboard{
		Connections: make([]Connection, 0),
		MaxLines:    maxLines,
	}
	debugLog("Dashboard: Created new dashboard with maxLines=%d", maxLines)
	return d
}

func readHPFeedsConfig(filename string) (*HPFeedsConfig, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to open hpfeeds.conf file: %v", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	if !scanner.Scan() {
		return nil, fmt.Errorf("hpfeeds.conf file is empty")
	}

	line := scanner.Text()
	parts := strings.Fields(line)
	if len(parts) != 5 {
		return nil, fmt.Errorf("invalid hpfeeds.conf format, expected 5 parts: ident secret server port channel")
	}

	return &HPFeedsConfig{
		Ident:   parts[0],
		Secret:  parts[1],
		Server:  parts[2],
		Port:    parts[3],
		Channel: parts[4],
	}, nil
}

func NewHpfeeds(ident, auth, host string, port int) *Hpfeeds {
	return &Hpfeeds{
		ident:        ident,
		auth:         auth,
		host:         host,
		port:         port,
		channel:      make(map[string]chan HPFeedsMessage),
		authSent:     make(chan bool),
		Disconnected: make(chan error),
		Log:          false,
	}
}

func (hp *Hpfeeds) Connect() error {
	addr, err := net.ResolveTCPAddr("tcp", fmt.Sprintf("%s:%d", hp.host, hp.port))
	if err != nil {
		return err
	}
	hp.conn, err = net.DialTCP("tcp", &hp.LocalAddr, addr)
	if err != nil {
		return err
	}
	go hp.readLoop()
	<-hp.authSent
	return nil
}

func (hp *Hpfeeds) close(err error) {
	if hp.conn != nil {
		hp.conn.Close()
		hp.conn = nil
	}
	select {
	case hp.Disconnected <- err:
	default:
	}
}

func (hp *Hpfeeds) readLoop() {
	buf := make([]byte, 0, 1024)
	for {
		tmpbuf := make([]byte, 1024)
		n, err := hp.conn.Read(tmpbuf)
		if err != nil {
			hp.close(err)
			return
		}
		buf = append(buf, tmpbuf[:n]...)
		for len(buf) >= 5 {
			var hdr rawMsgHeader
			hdr.Length = binary.BigEndian.Uint32(buf)
			hdr.Opcode = uint8(buf[4])
			if len(buf) < int(hdr.Length) {
				break
			}
			data := buf[5:int(hdr.Length)]
			hp.parse(hdr.Opcode, data)
			buf = buf[int(hdr.Length):]
		}
	}
}

func (hp *Hpfeeds) parse(opcode uint8, data []byte) {
	switch opcode {
	case opcode_info:
		hp.sendAuth(data[(1 + uint8(data[0])):])
		hp.authSent <- true
	case opcode_err:
		debugLog("Received error from server: %s", string(data))
	case opcode_pub:
		len1 := uint8(data[0])
		name := string(data[1:(1 + len1)])
		len2 := uint8(data[1+len1])
		channel := string(data[(1 + len1 + 1):(1 + len1 + 1 + len2)])
		payload := data[1+len1+1+len2:]
		hp.handlePub(name, channel, payload)
	default:
		debugLog("Received message with unknown type %d", opcode)
	}
}

func (hp *Hpfeeds) handlePub(name string, channelName string, payload []byte) {
	// Note that this hpfeeds implementation has only been tested with the cowrie.sessions channel
	debugLog("HPFeeds: Received message from %s on channel %s: %s", name, channelName, string(payload))
	channel, ok := hp.channel[channelName]
	if !ok {
		debugLog("HPFeeds: Message received on unsubscribed channel %s", channelName)
		return
	}
	channel <- HPFeedsMessage{name, payload}
}

func writeField(buf *bytes.Buffer, data []byte) {
	buf.WriteByte(byte(len(data)))
	buf.Write(data)
}

func (hp *Hpfeeds) sendRawMsg(opcode uint8, data []byte) {
	buf := make([]byte, 5)
	binary.BigEndian.PutUint32(buf, uint32(5+len(data)))
	buf[4] = byte(opcode)
	buf = append(buf, data...)
	for len(buf) > 0 {
		n, err := hp.conn.Write(buf)
		if err != nil {
			debugLog("Write(): %s", err)
			hp.close(err)
			return
		}
		buf = buf[n:]
	}
}

func (hp *Hpfeeds) sendAuth(nonce []byte) {
	buf := new(bytes.Buffer)
	mac := sha1.New()
	mac.Write(nonce)
	mac.Write([]byte(hp.auth))
	writeField(buf, []byte(hp.ident))
	buf.Write(mac.Sum(nil))
	hp.sendRawMsg(opcode_auth, buf.Bytes())
}

func (hp *Hpfeeds) sendSub(channelName string) {
	buf := new(bytes.Buffer)
	writeField(buf, []byte(hp.ident))
	buf.Write([]byte(channelName))
	hp.sendRawMsg(opcode_sub, buf.Bytes())
}

func (hp *Hpfeeds) Subscribe(channelName string, channel chan HPFeedsMessage) {
	hp.channel[channelName] = channel
	hp.sendSub(channelName)
}

func generateRandomIP() string {
	// Randomized IP addresses for mock connections
	return fmt.Sprintf("%d.%d.%d.%d",
		rand.Intn(256), rand.Intn(256), rand.Intn(256), rand.Intn(256))
}

func generateRandomUsername() string {
	// Some convincing usernames for mock connections
	usernames := []string{
		"admin", "root", "user", "guest", "test", "demo", "backup", "service",
		"operator", "manager", "support", "dev", "prod", "staging", "www",
		"ftp", "mail", "database", "oracle", "mysql", "postgres", "redis",
		"nginx", "apache", "tomcat", "jenkins", "docker", "k8s", "elastic",
	}
	return usernames[rand.Intn(len(usernames))]
}

func generateRandomPassword() string {
	// Some popular weak passwords for mock connections
	passwords := []string{
		"123456", "password", "admin", "root", "guest", "test", "demo",
		"letmein", "welcome", "monkey", "dragon", "qwerty", "abc123",
		"password123", "admin123", "root123", "guest123", "test123",
		"changeme", "default", "pass", "secret", "master", "super",
		"manager", "system", "operator", "backup", "service", "temp",
	}
	return passwords[rand.Intn(len(passwords))]
}

func debugLog(format string, v ...interface{}) {
	if debugLogger != nil {
		debugLogger.Printf(format, v...)
	}
}

func NewTUI(aspectRatio float64) (*TUI, error) {
	screen, err := tcell.NewScreen()
	if err != nil {
		return nil, err
	}

	if err := screen.Init(); err != nil {
		return nil, err
	}

	screen.SetStyle(tcell.StyleDefault.Background(colorBackground).Foreground(colorText))
	screen.Clear()

	width, height := screen.Size()

	tui := &TUI{
		screen:       screen,
		width:        width,
		height:       height,
		globeChanged: true,
		dashChanged:  true,
		statsChanged: true,
		aspectRatio:  aspectRatio,
	}

	// Dashboard is fixed at exactly 45 characters wide. This was chosen as it leaves an
	// approximately square area for the globe on an 80x24 terminal.
	dashboardWidth := 45
	// Globe gets remaining space: total width - dashboard width - separator (1 char) - padding (2 chars)
	globeWidth := width - dashboardWidth - 3

	// For small terminals (like 80x24), ensure globe gets at least some space
	if globeWidth < 10 {
		globeWidth = 10
	}

	tui.globe = NewGlobe(globeWidth, height, aspectRatio)
	// Reserve 4 lines for stats header and chart at bottom
	tui.dashboard = NewDashboard(height - 4)
	tui.stats = NewStatsManager()

	debugLog("TUI: Initialized with size %dx%d (globe: %d, dashboard: 45)", width, height, globeWidth)
	return tui, nil
}

func (tui *TUI) Close() {
	if tui.screen != nil {
		tui.screen.Fini()
	}
}

func (tui *TUI) HandleResize() {
	tui.mutex.Lock()
	defer tui.mutex.Unlock()

	tui.width, tui.height = tui.screen.Size()

	// Dashboard is fixed at exactly 45 characters wide
	dashboardWidth := 45
	// Globe gets remaining space: total width - dashboard width - separator (1 char) - padding (2 chars)
	globeWidth := tui.width - dashboardWidth - 3

	// For small terminals (like 80x24), ensure globe gets at least some space
	if globeWidth < 10 {
		globeWidth = 10
	}

	tui.globe = NewGlobe(globeWidth, tui.height, tui.aspectRatio)

	// Update dashboard MaxLines without creating a new instance (preserve shared reference)
	// Reserve 4 lines for stats header and chart at bottom
	if tui.dashboard != nil {
		tui.dashboard.mutex.Lock()
		newMaxLines := tui.height - 4
		tui.dashboard.MaxLines = newMaxLines
		// Trim connections if necessary
		if len(tui.dashboard.Connections) > newMaxLines {
			tui.dashboard.Connections = tui.dashboard.Connections[len(tui.dashboard.Connections)-newMaxLines:]
		}
		tui.dashboard.mutex.Unlock()
	}

	tui.globeChanged = true
	tui.dashChanged = true
	tui.statsChanged = true
	tui.screen.Clear()

	debugLog("TUI: Resized to %dx%d (globe: %d, dashboard: 45)", tui.width, tui.height, globeWidth)
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
	for i, r := range []rune(text) {
		if x+i >= tui.width {
			break
		}
		tui.screen.SetContent(x+i, y, r, nil, style)
	}
}

func (tui *TUI) renderGlobe(rotation float64) {
	tui.mutex.RLock()
	changed := tui.globeChanged
	tui.mutex.RUnlock()

	if !changed {
		return
	}

	globeScreen := tui.globe.render(rotation)
	landStyle := tcell.StyleDefault.Foreground(colorGlobe)
	attackStyle := tcell.StyleDefault.Foreground(colorAttack).Bold(true)

	// Clear globe area
	for y := 0; y < tui.globe.Height; y++ {
		for x := 0; x < tui.globe.Width; x++ {
			tui.screen.SetContent(x, y, ' ', nil, tcell.StyleDefault)
		}
	}

	// Draw globe with special styling for attack locations
	for y := 0; y < len(globeScreen) && y < tui.height; y++ {
		for x := 0; x < len(globeScreen[y]) && x < tui.globe.Width; x++ {
			char := globeScreen[y][x]
			if char != ' ' {
				// Use red color for asterisks (attack locations), green for land
				if char == '*' {
					tui.screen.SetContent(x, y, char, nil, attackStyle)
				} else {
					tui.screen.SetContent(x, y, char, nil, landStyle)
				}
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

	// Calculate dashboard height (reserve 4 lines for stats header and chart at bottom)
	dashboardHeight := tui.height - 4
	dashLines := tui.dashboard.Render(dashboardHeight)
	separatorX := tui.globe.Width + 1
	startX := separatorX + 2 // Dashboard starts 2 chars after separator
	dashboardWidth := 45     // Fixed dashboard width

	// Clear dashboard area only (not the stats area)
	for y := 0; y < dashboardHeight; y++ {
		// Clear separator
		tui.screen.SetContent(separatorX, y, ' ', nil, tcell.StyleDefault)
		// Clear dashboard area
		for x := 0; x < dashboardWidth && startX+x < tui.width; x++ {
			tui.screen.SetContent(startX+x, y, ' ', nil, tcell.StyleDefault)
		}
	}

	// Draw separator (full height)
	for y := 0; y < tui.height; y++ {
		tui.screen.SetContent(separatorX, y, '|', nil,
			tcell.StyleDefault.Foreground(colorSeparator))
	}

	// Draw dashboard content
	headerStyle := tcell.StyleDefault.Foreground(colorDashboard).Bold(true)
	connectionStyle := tcell.StyleDefault.Foreground(colorStats)
	statusOkStyle := tcell.StyleDefault.Foreground(colorStatusOk).Bold(true)
	statusErrorStyle := tcell.StyleDefault.Foreground(colorStatusError).Bold(true)

	for y, line := range dashLines {
		if y >= dashboardHeight {
			break
		}

		// Ensure line is exactly the right length (dashboard should already format correctly)
		if len(line) > dashboardWidth {
			line = line[:dashboardWidth]
		}

		style := connectionStyle
		if y <= 1 { // Header lines (now 2 lines: title with status, separator)
			style = headerStyle
		}

		// Only draw if we have space in terminal
		if startX < tui.width {
			// Special handling for header line (line 0) to color the status indicators
			if y == 0 {
				// Draw the base header line with header style
				tui.drawText(startX, y, line, style)

				// Find and colorize status indicators
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

	// Draw stats header separator
	headerY := dashboardHeight
	if headerY < tui.height {
		headerText := "-=-=-=-=- [ HOURLY ATTACK STATS ] -=-=-=-=-"
		headerStyle := tcell.StyleDefault.Foreground(colorDashboard).Bold(true)
		// Center the header text within the dashboard width
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

	// Position chart at far right edge of screen
	statsLines := tui.stats.RenderBarGraph(24) // Fixed 24-hour chart width
	if len(statsLines) == 0 || len(statsLines[0]) == 0 {
		return // No data to render
	}

	chartWidth := len(statsLines[0])     // Get actual width including labels
	startX := tui.width - chartWidth - 7 // Position 7 characters left from far right
	if startX < 0 {
		startX = 0 // Ensure we don't go off screen
	}

	// Stats area starts at the bottom of the screen (last 3 lines)
	statsStartY := tui.height - 3

	// Clear stats area (3 lines)
	clearStyle := tcell.StyleDefault.Background(colorBackground).Foreground(colorStats)
	for y := statsStartY; y < statsStartY+3 && y < tui.height; y++ {
		for x := startX; x < startX+chartWidth && x < tui.width; x++ {
			tui.screen.SetContent(x, y, ' ', nil, clearStyle)
		}
	}

	textStyle := tcell.StyleDefault.Background(colorBackground).Foreground(colorStats)

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

func (tui *TUI) Render(rotation float64) {
	tui.renderGlobe(rotation)
	tui.renderDashboard()
	tui.renderStats()
	tui.screen.Show()
}

var globalTUI *TUI

func (d *Dashboard) AddConnection(ip, username, password string) {
	if d == nil {
		debugLog("Dashboard: ERROR - Dashboard is nil!")
		return
	}

	d.mutex.Lock()
	defer d.mutex.Unlock()

	connection := Connection{
		IP:       ip,
		Username: username,
		Password: password,
		Time:     time.Now(),
	}

	d.Connections = append(d.Connections, connection)

	// Keep only the most recent MaxLines connections
	if len(d.Connections) > d.MaxLines {
		d.Connections = d.Connections[len(d.Connections)-d.MaxLines:]
	}

	// Mark dashboard as changed for TUI
	if globalTUI != nil {
		globalTUI.MarkDashboardChanged()
	} else {
		debugLog("Dashboard: ERROR - globalTUI is nil!")
	}
}

func (d *Dashboard) GenerateRandomConnection() {
	ip := generateRandomIP()
	username := generateRandomUsername()
	password := generateRandomPassword()
	d.AddConnection(ip, username, password)
}

func startHPFeedsClient(config *HPFeedsConfig, dashboard *Dashboard) error {
	port, err := strconv.Atoi(config.Port)
	if err != nil {
		return fmt.Errorf("invalid port: %v", err)
	}

	hp := NewHpfeeds(config.Ident, config.Secret, config.Server, port)

	err = hp.Connect()
	if err != nil {
		return fmt.Errorf("failed to connect to HPFeeds: %v", err)
	}

	debugLog("HPFeeds: Connected successfully, subscribing to channel '%s'", config.Channel)

	firehose := make(chan HPFeedsMessage)
	hp.Subscribe(config.Channel, firehose)

	debugLog("HPFeeds: Subscribed to channel, starting message handler")

	go func() {
		msgCount := 0
		for msg := range firehose {
			msgCount++
			debugLog("HPFeeds: Received message #%d from %s", msgCount, msg.Name)

			var session CowrieSession
			if err := json.Unmarshal(msg.Payload, &session); err == nil {
				// Successfully parsed cowrie session data
				var username, password string

				// Extract username/password from loggedin array [username, password]
				if len(session.LoggedIn) >= 2 {
					username = session.LoggedIn[0]
					password = session.LoggedIn[1]
				}

				// Use peerIP as the IP address
				ipAddress := session.PeerIP

				// Lookup geolocation information (debug logging happens in LookupIP function)
				//var locationInfo LocationInfo
				//if globalGeoIP != nil && ipAddress != "" {
				//	locationInfo := globalGeoIP.LookupIP(ipAddress)
				//}

				if ipAddress != "" {
					if username != "" && password != "" {
						debugLog("HPFeeds: Adding successful login to dashboard")
						dashboard.AddConnection(ipAddress, username, password)
					} else {
						debugLog("HPFeeds: Adding connection attempt (no successful login) to dashboard")
						dashboard.AddConnection(ipAddress, "connection", session.Protocol)
					}
				} else {
					debugLog("HPFeeds: Skipping session data - no peerIP address")
				}
			} else {
				debugLog("HPFeeds: Failed to parse JSON payload: %v", err)
				debugLog("HPFeeds: Raw payload: %s", string(msg.Payload))
			}
		}
		debugLog("HPFeeds: Message handler goroutine exited")
	}()

	// Handle disconnection in a separate goroutine
	go func() {
		<-hp.Disconnected
		debugLog("HPFeeds connection lost, falling back to mock data")
		globalHPFeedsConnected = false
	}()

	return nil
}

// Kinda hacky, but it works.
func (d *Dashboard) Render(height int) []string {
	d.mutex.RLock()
	defer d.mutex.RUnlock()

	lines := make([]string, height)

	// Header with status indicators - fit within 45 chars
	hpfeedsStatus := "!"
	if globalHPFeedsConnected {
		hpfeedsStatus = "+"
	}
	geoipStatus := "!"
	if globalGeoIPAvailable {
		geoipStatus = "+"
	}
	headerLine := fmt.Sprintf("SecKC MHN TUI | HPFeeds [%s]  GeoIP [%s]", hpfeedsStatus, geoipStatus)
	// Ensure it fits within 45 chars (should be 52 chars)
	if len(headerLine) > 45 {
		headerLine = headerLine[:45]
	}
	lines[0] = headerLine
	lines[1] = strings.Repeat("-", 45)

	// Connection entries
	startLine := 2
	for i, conn := range d.Connections {
		if startLine+i >= height-1 {
			break
		}

		// Format: "  192.168.1.1 - username:password"
		// IP gets 15 chars, separator 3 chars, credentials get remainder
		ipPart := fmt.Sprintf("%15s", conn.IP)
		credPart := fmt.Sprintf("%s:%s", conn.Username, conn.Password)

		// Total available space: 45 chars
		// IP + " - " = 18 chars, leaving 35 for credentials
		if len(credPart) > 27 {
			credPart = credPart[:27]
		}

		line := fmt.Sprintf("%s - %s", ipPart, credPart)

		// Ensure line doesn't exceed 45 characters
		if len(line) > 45 {
			line = line[:45]
		}

		lines[startLine+i] = line
	}

	// Fill remaining lines with empty strings
	for i := startLine + len(d.Connections); i < height; i++ {
		lines[i] = ""
	}

	return lines
}

func getEarthBitmap() []string {
	// Accurate ASCII bitmap of Earth using equirectangular projection
	// Converted from PNG data with utils/convert_png.go
	// 120 characters wide, 60 rows
	// Longitude: -180 to +180, Latitude: +90 to -90 (top to bottom)
	// '#' = land, ' ' = ocean
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

func (g *Globe) sampleEarthAt(lat, lon float64) rune {
	// Convert lat/lon to bitmap coordinates
	// Latitude: -90 to +90 maps to 0 to MapHeight-1
	// Longitude: -180 to +180 maps to 0 to MapWidth-1

	// Normalize latitude from -90..90 to 0..1
	latNorm := (lat + 90) / 180
	// Normalize longitude from -180..180 to 0..1
	lonNorm := (lon + 180) / 360

	// Convert to bitmap coordinates
	y := int(latNorm * float64(g.MapHeight-1))
	x := int(lonNorm * float64(g.MapWidth-1))

	// Clamp to valid range
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
	// Adjust longitude to better match Earth bitmap coordinate system
	adjustedLon := lon - 70
	// Convert lat/lon to 3D coordinates
	latRad := lat * math.Pi / 180
	lonRad := (adjustedLon + rotation*180/math.Pi) * math.Pi / 180

	x := math.Cos(latRad) * math.Cos(lonRad)
	y := math.Sin(latRad)
	z := math.Cos(latRad) * math.Sin(lonRad)

	// Check if point is on the visible hemisphere
	if z < 0 {
		return 0, 0, false
	}

	// Project to 2D screen coordinates
	screenX := int(x*g.Radius) + g.Width/2
	screenY := int(-y*g.Radius/g.AspectRatio) + g.Height/2 // Compress Y by aspect ratio for character aspect ratio

	// Check bounds
	if screenX < 0 || screenX >= g.Width || screenY < 0 || screenY >= g.Height {
		return 0, 0, false
	}

	return screenX, screenY, true
}

func (g *Globe) render(rotation float64) [][]rune {
	// Safety checks to prevent panics
	if g.Width <= 0 || g.Height <= 0 {
		// Return minimal screen for invalid dimensions
		return [][]rune{[]rune{' '}}
	}

	screen := make([][]rune, g.Height)
	for i := range screen {
		screen[i] = make([]rune, g.Width)
		for j := range screen[i] {
			screen[i][j] = ' '
		}
	}

	// Create a separate layer for attack locations (red asterisks)
	attackLayer := make([][]bool, g.Height)
	for i := range attackLayer {
		attackLayer[i] = make([]bool, g.Width)
	}

	// Collect unique IP addresses from dashboard and render attack locations
	if globalTUI != nil && globalTUI.dashboard != nil && globalGeoIP != nil {
		uniqueIPs := make(map[string]bool)

		globalTUI.dashboard.mutex.RLock()
		for _, conn := range globalTUI.dashboard.Connections {
			uniqueIPs[conn.IP] = true
		}
		globalTUI.dashboard.mutex.RUnlock()

		// For each unique IP, get geolocation and project onto globe
		for ipStr := range uniqueIPs {
			locationInfo := globalGeoIP.LookupIP(ipStr)
			if locationInfo.Valid {
				// Project lat/lon to screen coordinates
				screenX, screenY, visible := g.project3DTo2D(locationInfo.Latitude, locationInfo.Longitude, rotation)
				if visible && screenX >= 0 && screenX < g.Width && screenY >= 0 && screenY < g.Height {
					attackLayer[screenY][screenX] = true
				}
			}
		}
	}

	// Create a density map for anti-aliasing
	density := make([][]float64, g.Height)
	for i := range density {
		density[i] = make([]float64, g.Width)
	}

	centerX, centerY := g.Width/2, g.Height/2

	// Sample the globe at each screen position
	for y := 0; y < g.Height; y++ {
		for x := 0; x < g.Width; x++ {
			// Calculate distance from center, accounting for character aspect ratio
			dx := float64(x - centerX)
			dy := float64(y-centerY) * g.AspectRatio // Compress Y for character aspect ratio
			distance := math.Sqrt(dx*dx + dy*dy)

			if distance <= g.Radius {
				// Convert screen position back to lat/lon
				// Normalize to sphere coordinates
				nx := dx / g.Radius
				ny := dy / g.Radius

				// Check if we're within the visible hemisphere
				nz_squared := 1 - nx*nx - ny*ny
				if nz_squared >= 0 {
					nz := math.Sqrt(nz_squared)

					// Convert back to lat/lon
					lat := math.Asin(ny) * 180 / math.Pi
					lon := math.Atan2(nx, nz)*180/math.Pi + rotation*180/math.Pi

					// Normalize longitude
					for lon < -180 {
						lon += 360
					}
					for lon > 180 {
						lon -= 360
					}

					// Sample the Earth bitmap at this position
					earthChar := g.sampleEarthAt(lat, lon)
					if earthChar != ' ' {
						// Different characters represent different terrain densities
						switch earthChar {
						case '#':
							density[y][x] += 1.0 // Dense land
						case '.':
							density[y][x] += 0.6 // Medium land
						default:
							density[y][x] += 0.8 // Other land characters
						}

						// Add some anti-aliasing around land edges
						for dy := -1; dy <= 1; dy++ {
							for dx := -1; dx <= 1; dx++ {
								nx2, ny2 := x+dx, y+dy
								if nx2 >= 0 && nx2 < g.Width && ny2 >= 0 && ny2 < g.Height {
									density[ny2][nx2] += 0.05
								}
							}
						}
					}
				}
			}

			// Add circular border for the sphere
			if distance > g.Radius-0.5 && distance < g.Radius+0.5 {
				density[y][x] += 0.2
			}
		}
	}

	// Convert density to ASCII art characters with anti-aliasing
	for y := 0; y < g.Height; y++ {
		for x := 0; x < g.Width; x++ {
			// First, render the base geography
			d := density[y][x]
			if d > 1.0 {
				screen[y][x] = '@' // Very dense land
			} else if d > 0.8 {
				screen[y][x] = '#' // Dense land
			} else if d > 0.6 {
				screen[y][x] = '%' // Medium-dense land
			} else if d > 0.4 {
				screen[y][x] = 'o' // Medium land
			} else if d > 0.3 {
				screen[y][x] = '=' // Light-medium
			} else if d > 0.2 {
				screen[y][x] = '+' // Light (coastlines/borders)
			} else if d > 0.15 {
				screen[y][x] = '-' // Very light
			} else if d > 0.1 {
				screen[y][x] = '.' // Minimal
			} else if d > 0.05 {
				screen[y][x] = '`' // Trace
			}

			// Overlay attack locations as asterisks (will render as red in TUI)
			if attackLayer[y][x] {
				screen[y][x] = '*'
			}
		}
	}

	return screen
}

func (tui *TUI) pollEvents() chan bool {
	quit := make(chan bool, 1)
	go func() {
		for {
			ev := tui.screen.PollEvent()
			switch ev := ev.(type) {
			case *tcell.EventKey:
				debugLog("TUI: Key pressed - %s", ev.Name())
				switch ev.Key() {
				case tcell.KeyCtrlC:
					debugLog("TUI: Ctrl+C pressed, exiting")
					quit <- true
					return
				case tcell.KeyEscape:
					debugLog("TUI: Escape key pressed, exiting")
					quit <- true
					return
				case tcell.KeyRune:
					r := ev.Rune()
					if r == 'q' || r == 'Q' || r == 'x' || r == 'X' || r == ' ' {
						debugLog("TUI: Exit key %c pressed, exiting", r)
						quit <- true
						return
					}
				}
			case *tcell.EventResize:
				debugLog("TUI: Resize event detected")
				tui.HandleResize()
			}
		}
	}()
	return quit
}

func showHelp() {
	fmt.Printf(`SecKC-MHN-Globe - TUI Earth visualization with honeypot monitoring

DESCRIPTION:
    Terminal-based application displaying a rotating 3D ASCII globe with a live
    dashboard of incoming connection attempts. Connects to HPFeeds (honeypot 
    data feeds) to show real-time attack data from security honeypots worldwide.

USAGE:
    go-globe [OPTIONS]

OPTIONS:
    -h               Show this help message 
    -d <filename>    Enable debug logging to specified file
    -s <seconds>     Globe rotation period in seconds (10-300, default: 30)
    -r <milliseconds> Globe refresh rate in milliseconds (50-1000, default: 100)
    -m               Enable monochrome mode (all colors set to white)
    -a <ratio>       Character aspect ratio (height/width, 1.0-4.0, default: 2.0)

CONTROLS:
    Q, X, Space, Esc    Exit the application

CONFIGURATION:
    Optional: Place HPFeeds credentials in 'hpfeeds.conf' with format:
    <ident> <secret> <server> <port> <channel>

	Mock data is generated if HPFeeds is unconfigured or unavailable.
    
	Optional: Place GeoLite2-City.mmdb in current directory for IP geolocation
	Register for free download from: https://www.maxmind.com/en/geolite2/signup

	Globe spins without any locations marked if GeoIP database is not available.


	EXAMPLES:
	go-globe                    # Default 30-second rotation, 100ms refresh, 2.0 aspect ratio
	go-globe -s 60              # Slower 60-second rotation  
	go-globe -s 10 -d debug.log # Fast rotation with debug logging
	go-globe -r 200             # Slower 200ms refresh rate
	go-globe -s 15 -r 50        # Fast rotation with fast 50ms refresh
	go-globe -m                 # Monochrome mode for terminals with limited colors
	go-globe -a 1.5             # Wider globe for narrow characters (aspect ratio 1.5)
	go-globe -a 3.0             # Narrower globe for wide characters (aspect ratio 3.0)

	`)
}

func main() {
	var debugFile = flag.String("d", "", "Debug log filename")
	var showHelpFlag = flag.Bool("h", false, "Show help")
	var rotationPeriod = flag.Int("s", 30, "Globe rotation period in seconds (10-300)")
	var refreshRate = flag.Int("r", 100, "Globe refresh rate in milliseconds (50-1000)")
	var monochrome = flag.Bool("m", false, "Enable monochrome mode (all colors set to white)")
	var aspectRatio = flag.Float64("a", 2.0, "Character aspect ratio (height/width, 1.0-4.0)")

	flag.Parse()

	if *showHelpFlag {
		showHelp()
		os.Exit(0)
	}

	// Validate rotation period
	if *rotationPeriod < 10 || *rotationPeriod > 300 {
		fmt.Fprintf(os.Stderr, "Error: Rotation period must be between 10 and 300 seconds\n")
		os.Exit(1)
	}

	// Validate refresh rate
	if *refreshRate < 50 || *refreshRate > 1000 {
		fmt.Fprintf(os.Stderr, "Error: Refresh rate must be between 50 and 1000 milliseconds\n")
		os.Exit(1)
	}

	// Validate aspect ratio
	if *aspectRatio < 1.0 || *aspectRatio > 4.0 {
		fmt.Fprintf(os.Stderr, "Error: Aspect ratio must be between 1.0 and 4.0\n")
		os.Exit(1)
	}

	if *debugFile != "" {
		file, err := os.OpenFile(*debugFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: Cannot open debug log file '%s': %v\n", *debugFile, err)
			os.Exit(1)
		}
		defer file.Close()
		debugLogger = log.New(file, "", log.LstdFlags|log.Lmicroseconds)
		debugLog("Debug logging started for go-globe")
	}

	debugLog("Rotation period set to %d seconds", *rotationPeriod)
	debugLog("Globe refresh rate set to %d milliseconds", *refreshRate)

	// Initialize colors based on monochrome flag
	initializeColors(*monochrome)
	if *monochrome {
		debugLog("Monochrome mode enabled")
	} else {
		debugLog("Color mode enabled")
	}

	rand.Seed(time.Now().UnixNano())
	debugLog("Application starting up")

	// Initialize GeoIP manager
	geoIPManager := NewGeoIPManager()
	defer func() {
		if geoIPManager != nil {
			geoIPManager.Close()
		}
	}()

	// Try to load GeoIP database (silently disable if not found)
	geoIPFile := "GeoLite2-City.mmdb"
	if _, err := os.Stat(geoIPFile); err == nil {
		if err := geoIPManager.LoadDatabase(geoIPFile); err != nil {
			debugLog("GeoIP: Failed to load database: %v", err)
			globalGeoIP = nil // Disable GeoIP functionality
		} else {
			debugLog("GeoIP: Database loaded successfully")
			globalGeoIP = geoIPManager // Enable GeoIP functionality
			globalGeoIPAvailable = true
		}
	} else {
		debugLog("GeoIP: Database file not found, GeoIP functionality disabled")
		globalGeoIP = nil // Disable GeoIP functionality
	}

	// Initialize TUI
	tui, err := NewTUI(*aspectRatio)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error initializing TUI: %v\n", err)
		os.Exit(1)
	}
	defer tui.Close()

	globalTUI = tui // Set global reference for dashboard updates

	// Start event listener
	quit := tui.pollEvents()

	// Create a shared dashboard instance for both TUI and HPFeeds
	sharedDashboard := NewDashboard(tui.height - 4) // Reserve space for stats header and chart
	tui.dashboard = sharedDashboard
	debugLog("Created shared dashboard at pointer: %p", sharedDashboard)

	// Try to load HPFeeds configuration and start client
	config, err := readHPFeedsConfig("hpfeeds.conf")
	useLiveData := false
	if err != nil {
		debugLog("HPFeeds config file not found: %v", err)
		debugLog("Using default SecKC MHN public credentials")
		// Use default SecKC MHN credentials
		config = &HPFeedsConfig{
			Ident:   "seckc-community",
			Secret:  "fk6QgrnyvwbWSxCIwL5SIc2oARC4DXx46",
			Server:  "mhn.h-i-r.net",
			Port:    "10000",
			Channel: "cowrie.sessions",
		}
	}

	debugLog("HPFeeds config: server=%s:%s channel=%s ident=%s", config.Server, config.Port, config.Channel, config.Ident)
	debugLog("Dashboard pointer for HPFeeds: %p", sharedDashboard)
	err = startHPFeedsClient(config, sharedDashboard)
	if err != nil {
		debugLog("HPFeeds client connection failed: %v", err)
	} else {
		debugLog("HPFeeds client connected successfully")
		globalHPFeedsConnected = true
		useLiveData = true
	}

	startTime := time.Now()
	lastConnectionTime := time.Now()
	lastGlobeUpdate := time.Now()
	lastStatsUpdate := time.Now()

	// Random interval for mock data generation (0.2 to 5 seconds)
	nextMockInterval := time.Duration(200+rand.Intn(4800)) * time.Millisecond

	// Fetch initial stats data
	go func() {
		if err := tui.stats.FetchData(); err != nil {
			debugLog("Stats: Initial fetch failed: %v", err)
		} else {
			tui.MarkStatsChanged()
		}
	}()

	// Main rendering loop
	for {
		select {
		case <-quit:
			debugLog("Quit signal received, shutting down")
			tui.Close()
			fmt.Println("Exiting...")
			os.Exit(0)
		default:
			// Continue with normal operation
		}

		now := time.Now()

		// Update globe rotation (mark as changed based on configurable refresh rate)
		if now.Sub(lastGlobeUpdate) >= time.Duration(*refreshRate)*time.Millisecond {
			tui.MarkGlobeChanged()
			lastGlobeUpdate = now
		}

		// Generate mock data if not using live HPFeeds data
		if !useLiveData && now.Sub(lastConnectionTime) >= nextMockInterval {
			tui.dashboard.GenerateRandomConnection()
			lastConnectionTime = now
			// Generate new random interval for next mock data (0.2 to 5 seconds)
			nextMockInterval = time.Duration(200+rand.Intn(4800)) * time.Millisecond
		}

		// Update stats data every 5 minutes
		if now.Sub(lastStatsUpdate) >= 300*time.Second {
			go func() {
				if err := tui.stats.FetchData(); err != nil {
					debugLog("Stats: Periodic fetch failed: %v", err)
				} else {
					tui.MarkStatsChanged()
				}
			}()
			lastStatsUpdate = now
		}

		// Calculate rotation using configurable period
		elapsed := now.Sub(startTime).Seconds()
		rotation := -(elapsed / float64(*rotationPeriod)) * 2 * math.Pi // Complete rotation every N seconds

		// Render (only updates changed areas)
		tui.Render(rotation)

		time.Sleep(50 * time.Millisecond) // Smoother animation
	}
}
