package main

import (
	"bufio"
	"bytes"
	"crypto/sha1"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math"
	"math/rand"
	"net"
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

var globalGeoIP *GeoIPManager
var globalHPFeedsConnected bool
var globalGeoIPAvailable bool

type TUI struct {
	screen       tcell.Screen
	width        int
	height       int
	globe        *Globe
	dashboard    *Dashboard
	globeChanged bool
	dashChanged  bool
	mutex        sync.RWMutex
}

type Globe struct {
	Radius    float64
	Width     int
	Height    int
	EarthMap  []string
	MapWidth  int
	MapHeight int
}

func NewGlobe(width, height int) *Globe {
	// Ensure minimum dimensions to prevent panics
	if width < 1 {
		width = 1
	}
	if height < 1 {
		height = 1
	}

	// Use provided dimensions directly (TUI now handles sizing)
	globeWidth := width
	effectiveHeight := float64(height) * 2
	radius := math.Min(float64(globeWidth)/2.5, effectiveHeight/2.5)

	// Ensure minimum radius
	if radius < 1.0 {
		radius = 1.0
	}

	earthMap := getEarthBitmap()
	return &Globe{
		Radius:    radius,
		Width:     globeWidth,
		Height:    height,
		EarthMap:  earthMap,
		MapWidth:  len(earthMap[0]),
		MapHeight: len(earthMap),
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
		if hp.Log {
			log.Printf("Received error from server: %s\n", string(data))
		}
	case opcode_pub:
		len1 := uint8(data[0])
		name := string(data[1:(1 + len1)])
		len2 := uint8(data[1+len1])
		channel := string(data[(1 + len1 + 1):(1 + len1 + 1 + len2)])
		payload := data[1+len1+1+len2:]
		hp.handlePub(name, channel, payload)
	default:
		if hp.Log {
			log.Printf("Received message with unknown type %d\n", opcode)
		}
	}
}

func (hp *Hpfeeds) handlePub(name string, channelName string, payload []byte) {
	// Note that this hpfeeds implementation has only been tested with the cowrie.sessions channel
	debugLog("HPFeeds: Received message from %s on channel %s: %s", name, channelName, string(payload))
	channel, ok := hp.channel[channelName]
	if !ok {
		if hp.Log {
			log.Printf("Received message on unsubscribed channel %s\n", channelName)
		}
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
			if hp.Log {
				log.Printf("Write(): %s\n", err)
			}
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

func NewTUI() (*TUI, error) {
	screen, err := tcell.NewScreen()
	if err != nil {
		return nil, err
	}

	if err := screen.Init(); err != nil {
		return nil, err
	}

	screen.SetStyle(tcell.StyleDefault.Background(tcell.ColorBlack).Foreground(tcell.ColorWhite))
	screen.Clear()

	width, height := screen.Size()

	tui := &TUI{
		screen:       screen,
		width:        width,
		height:       height,
		globeChanged: true,
		dashChanged:  true,
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

	tui.globe = NewGlobe(globeWidth, height)
	tui.dashboard = NewDashboard(height - 3)

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

	tui.globe = NewGlobe(globeWidth, tui.height)

	// Update dashboard MaxLines without creating a new instance (preserve shared reference)
	if tui.dashboard != nil {
		tui.dashboard.mutex.Lock()
		newMaxLines := tui.height - 3
		tui.dashboard.MaxLines = newMaxLines
		// Trim connections if necessary
		if len(tui.dashboard.Connections) > newMaxLines {
			tui.dashboard.Connections = tui.dashboard.Connections[len(tui.dashboard.Connections)-newMaxLines:]
		}
		tui.dashboard.mutex.Unlock()
	}

	tui.globeChanged = true
	tui.dashChanged = true
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
	landStyle := tcell.StyleDefault.Foreground(tcell.ColorGreen)
	attackStyle := tcell.StyleDefault.Foreground(tcell.ColorRed).Bold(true)

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

	dashLines := tui.dashboard.Render(tui.height)
	separatorX := tui.globe.Width + 1
	startX := separatorX + 2 // Dashboard starts 2 chars after separator
	dashboardWidth := 45     // Fixed dashboard width

	// Clear dashboard area - exactly 45 characters plus separator area
	for y := 0; y < tui.height; y++ {
		// Clear separator
		tui.screen.SetContent(separatorX, y, ' ', nil, tcell.StyleDefault)
		// Clear dashboard area
		for x := 0; x < dashboardWidth && startX+x < tui.width; x++ {
			tui.screen.SetContent(startX+x, y, ' ', nil, tcell.StyleDefault)
		}
	}

	// Draw separator
	for y := 0; y < tui.height; y++ {
		tui.screen.SetContent(separatorX, y, '|', nil,
			tcell.StyleDefault.Foreground(tcell.ColorGray))
	}

	// Draw dashboard content
	headerStyle := tcell.StyleDefault.Foreground(tcell.ColorYellow).Bold(true)
	connectionStyle := tcell.StyleDefault.Foreground(tcell.ColorAqua)
	statusOkStyle := tcell.StyleDefault.Foreground(tcell.ColorGreen).Bold(true)
	statusErrorStyle := tcell.StyleDefault.Foreground(tcell.ColorRed).Bold(true)

	for y, line := range dashLines {
		if y >= tui.height {
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

	tui.mutex.Lock()
	tui.dashChanged = false
	tui.mutex.Unlock()
}

func (tui *TUI) Render(rotation float64) {
	tui.renderGlobe(rotation)
	tui.renderDashboard()
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
	hp.Log = false // Don't log to avoid cluttering output

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
		log.Println("HPFeeds connection lost, falling back to mock data")
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
	screenY := int(-y*g.Radius/2) + g.Height/2 // Compress Y by factor of 2 for character aspect ratio

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
			dy := float64(y-centerY) * 2 // Compress Y for character aspect ratio
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
	go-globe                    # Default 30-second rotation
	go-globe -s 60              # Slower 60-second rotation  
	go-globe -s 10 -d debug.log # Fast rotation with debug logging

	`)
}

func main() {
	var debugFile = flag.String("d", "", "Debug log filename")
	var showHelpFlag = flag.Bool("h", false, "Show help")
	var rotationPeriod = flag.Int("s", 30, "Globe rotation period in seconds (10-300)")

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
	tui, err := NewTUI()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error initializing TUI: %v\n", err)
		os.Exit(1)
	}
	defer tui.Close()

	globalTUI = tui // Set global reference for dashboard updates

	// Start event listener
	quit := tui.pollEvents()

	// Create a shared dashboard instance for both TUI and HPFeeds
	sharedDashboard := NewDashboard(tui.height - 3)
	tui.dashboard = sharedDashboard
	debugLog("Created shared dashboard at pointer: %p", sharedDashboard)

	// Try to load HPFeeds configuration and start client
	config, err := readHPFeedsConfig("hpfeeds.conf")
	useLiveData := false
	if err != nil {
		log.Printf("Warning: Could not read HPFeeds config: %v. Using mock data.", err)
		debugLog("HPFeeds config error: %v", err)
	} else {
		debugLog("HPFeeds config loaded: server=%s:%s channel=%s", config.Server, config.Port, config.Channel)
		debugLog("Dashboard pointer for HPFeeds: %p", sharedDashboard)
		err = startHPFeedsClient(config, sharedDashboard)
		if err != nil {
			log.Printf("Warning: Could not start HPFeeds client: %v. Using mock data.", err)
			debugLog("HPFeeds client connection failed: %v", err)
		} else {
			log.Println("HPFeeds client started successfully. Showing live honeypot data.")
			debugLog("HPFeeds client connected successfully")
			globalHPFeedsConnected = true
			useLiveData = true
		}
	}

	startTime := time.Now()
	lastConnectionTime := time.Now()
	lastGlobeUpdate := time.Now()

	// Random interval for mock data generation (0.2 to 5 seconds)
	nextMockInterval := time.Duration(200+rand.Intn(4800)) * time.Millisecond

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

		// Update globe rotation (mark as changed every 100ms for smooth animation)
		if now.Sub(lastGlobeUpdate) >= 100*time.Millisecond {
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

		// Calculate rotation using configurable period
		elapsed := now.Sub(startTime).Seconds()
		rotation := -(elapsed / float64(*rotationPeriod)) * 2 * math.Pi // Complete rotation every N seconds

		// Render (only updates changed areas)
		tui.Render(rotation)

		time.Sleep(50 * time.Millisecond) // Smoother animation
	}
}
