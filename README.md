# SecKC-MHN-Globe

Inspired by [The SecKC MHN Cyber Attack Map](https://mhn.h-i-r.net/dash), which inspired [n0xa's SecKC-MHN-Globe](https://github.com/n0xa/SecKC-MHN-Globe), which inspired me to build this enhanced version.

**Enhanced version hacked by ringmast4r**

## TUI Earth Visualization with Honeypot Monitoring
![SecKC-MHN-Globe Animation](animation.gif)

Terminal-based application displaying a rotating 3D ASCII globe with a live dashboard of incoming connection attempts. Connects to HPFeeds (honeypot data feeds) to show real-time attack data from security honeypots worldwide.

![Terminal Interface](https://img.shields.io/badge/Interface-Terminal%20TUI-green)
![Go Version](https://img.shields.io/badge/Go-1.24.5-blue)
![License](https://img.shields.io/badge/License-BSD%202--Clause-blue)

## 📡 How This Works

**This tool doesn't monitor YOUR computer.** It connects to the **SecKC MHN (Modern Honey Network)** - a public honeypot project that collects attack data from honeypots (fake vulnerable servers) around the world.

**Here's how it works:**

1. **SecKC runs honeypots** - Fake servers that pretend to be vulnerable SSH, Telnet, HTTP servers
2. **Attackers find and attack them** - Bots/hackers try to break in with usernames/passwords
3. **The honeypots log everything** - IPs, credentials, protocols
4. **SecKC publishes this data** - Via their public API at `https://mhn.h-i-r.net/seckcapi`
5. **Your program displays it** - You're visualizing attacks on THEIR honeypots, not yours

**You're just a viewer of attack data** - you're not being attacked. The globe shows where attacks are coming from globally against the SecKC honeypot network. You're monitoring the internet's background noise of constant attacks happening 24/7.

## 📊 Understanding the Data

Each attack entry shows two lines of information:

**Example with ASN/Org data:**
```
185.220.101.45 [DE] root:admin123
AS24940 Hetzner Online GmbH
```

**What the data means:**

- **ASN (Autonomous System Number)** - A unique number identifying a network (e.g., AS15169)
- **Org (Organization)** - The company/ISP that owns that network (e.g., "Google LLC", "Hetzner Online GmbH")
  - Tells you **who owns the IP address block**

- **rDNS (Reverse DNS)** - The hostname that the IP address points back to
  - Like a domain name for that specific IP
  - Often shows the actual server name or ISP's naming scheme
  - Example: `crawl-66-249-66-1.googlebot.com` or `static.22.145.93.142.clients.your-server.de`

**The difference:**
```
66.249.66.1 [US] admin:password
AS15169 Google LLC          ← Shows who owns the network
```
vs
```
66.249.66.1 [US] admin:password
crawl-66-249-66-1.googlebot.com  ← Shows the specific server hostname
```

The display shows **whichever data is available first** - preferring ASN/Org, falling back to rDNS if ASN isn't available. Some IPs may show "..." if lookups timeout or the IP has no reverse DNS record.

## Features

### Visual Enhancements
- **Multiple Character Sets**: Choose between ASCII, Unicode blocks, or high-res Braille rendering (2-4x higher resolution)
- **9 Color Themes**: default, matrix, amber, solarized, nord, dracula, mono, rainbow, skittles - switch live with `T` key
- **Rainbow Theme**: Solid diagonal rainbow stripes (Red → Orange → Yellow → Green → Blue → Indigo → Violet)
- **Skittles Theme**: Randomized rainbow-colored globe with each character displaying vibrant colors like scattered candy
- **Globe Lighting & Shading**: Lambertian diffuse lighting with configurable sun position and auto-follow mode
- **Attack Arc Trails**: Bézier curve attack paths with fade effects and motion blur
- **Matrix Rain Effect**: Falling code columns with configurable density
- **CRT/Scanline Effects**: Retro phosphor glow and scanline dimming
- **Protocol Glyphs**: Visual icons showing attack types (# SSH, ~ Telnet, @ SMTP, : HTTP, % FTP)

### Real-time Data & Intelligence
- **Live Attack Visualization**: Attacks marked on globe with protocol-specific indicators
- **HPFeeds Integration**: Connect to honeypot data feeds for real-time threat intelligence
- **Enhanced Dashboard**: Single-line compact display with full attack details:
  - IP address (15 chars)
  - Country code [CC] (2-letter)
  - City name (12 chars)
  - Protocol (SSH, HTTP, etc)
  - Credentials (username:password)
  - Timestamp (HH:MM format)
  - ASN/Organization or rDNS (full length, uses all remaining space)
- **Dynamic Dashboard Width**: Auto-expands to use all available terminal space (minimum 50 chars)
- **Horizontal Scrolling**: Press `,` and `.` to scroll dashboard left/right to see full text
- **Scroll Indicators**: `◀` shows more content to the left, `▶` shows more content to the right
- **Full Text Display**: All organization and rDNS names displayed in full - just scroll to see them!
- **Geographic Mapping**: IP geolocation with MaxMind GeoLite2 database (LRU cached)
- **Country Code & City Display**: Shows 2-letter country code and city name for each attack IP
- **ASN & Organization**: Displays Autonomous System Number and ISP/organization info (live data only)
- **Reverse DNS Lookup**: Shows rDNS hostnames for attacking IPs (live data only)
- **Detailed Info Panel**: Press `I` to view full details of most recent attack (IP, City, Country, ASN, Org, rDNS, Protocol, Credentials, Timestamp)
- **Top Attackers Stats Panel**: Press `S` to view top 5 countries and top 5 ASNs
- **Top IP Addresses Panel**: Press `P` to view top 10 attacking IP addresses with attack counts and organization info
- **Hourly Attack Stats**: ASCII bar chart + sparklines showing 24-hour attack volume
- **Demo Storm Mode**: Simulated attack traffic generator for presentations (optimized, no ASN/rDNS lookups)

### Interactive Controls
- **Navigation**: Arrow keys to nudge view, `+`/`-` to zoom (0.5x-3.0x)
- **Playback**: `Space` to pause, `[`/`]` to adjust spin speed (0.1x-5.0x)
- **Visual Toggles**: `T` cycle themes, `L` toggle lighting, `G` toggle arcs, `R` toggle rain
- **Info Panels**: `I` detailed attack info, `S` top attackers stats, `P` top IP addresses
- **Dashboard Scrolling**: `,` scroll left, `.` scroll right, `H` reset to home
- **Command Guide**: Press `C` for onscreen quick reference at bottom of screen
- **Help Overlay**: Press `?` for full keyboard shortcuts
- **Dynamic Resize**: Seamlessly adapts to terminal window resizing (globe gets 60% width, dashboard 40%)

### Configuration & Recording
- **TOML Config Files**: Load settings from config file with CLI override support
- **Asciinema Recording**: Export sessions to shareable `.cast` files
- **Debug Logging**: Comprehensive logging for troubleshooting and analysis
- **Mock Data Fallback**: Generates simulated data when HPFeeds is unavailable
## Build

```bash
# Clone the repository
git clone https://github.com/n0xa/SecKC-MHN-Globe.git
cd SecKC-MHN-Globe

# Install dependencies
go mod tidy

# Build the enhanced version
go build SecKC-MHN-Globe-Enhanced.go

# Or build the original version
go build SecKC-MHN-Globe.go
```

## Quick Start

### Launch Commands

After navigating to the folder (`cd SecKC-MHN-Globe-main`):

```bash
# Simple demo with fake attack traffic
go run SecKC-MHN-Globe-Enhanced.go --demo-storm

# Matrix theme with all visual effects
go run SecKC-MHN-Globe-Enhanced.go --theme matrix --charset braille --rain --arcs curved --lighting --demo-storm

# Live monitoring (connects to real honeypot data)
go run SecKC-MHN-Globe-Enhanced.go

# Original simple version
go run SecKC-MHN-Globe.go
```

### 🎮 Interactive Keyboard Controls (While Running)

**Change Visuals in Real-Time:**
- `T` - Cycle themes (default → matrix → amber → solarized → nord → dracula → mono → rainbow → skittles)
- `L` - Toggle lighting/shading on globe
- `G` - Toggle attack arc trails
- `R` - Toggle Matrix rain effect

**View Attack Information:**
- `I` - Show/hide detailed attack info panel (shows most recent attack details)
- `S` - Show/hide top attackers statistics panel (top 5 countries and ASNs)
- `P` - Show/hide top IP addresses panel (top 10 attacking IPs with organization info)

**Dashboard Scrolling:**
- `,` - Scroll dashboard left (shows earlier part of long text)
- `.` - Scroll dashboard right (shows later part of long text)
- `H` - Reset scroll to home position
- **Scrolling works!** All text is fully displayed - just scroll to see it

**Navigation & Playback:**
- `Space` - Pause/resume globe rotation
- `[` / `]` - Decrease/increase spin speed
- `+` / `-` - Zoom in/out
- Arrow keys - Nudge globe view angle

**Help & Guides:**
- `C` - Show/hide command guide at bottom of screen (quick reference)
- `?` - Show/hide full help overlay with all controls

**Exit:**
- `Q`, `X`, `Esc`, or `Ctrl+C` - Quit application

## 🎨 Command Line Visual Options

**Character Sets (Resolution):**
```bash
--charset ascii       # Classic ASCII characters (default)
--charset blocks      # Unicode block characters (█▓▒░)
--charset braille     # High-resolution Braille (2-4x sharper) ⣿
```

**Themes:**
```bash
--theme default       # Classic green globe
--theme matrix        # Green Matrix hacker aesthetic
--theme amber         # Retro CRT orange
--theme solarized     # Low-contrast blue/teal
--theme nord          # Cool Scandinavian palette
--theme dracula       # Dark with neon accents
--theme mono          # High-contrast white
--theme rainbow       # Solid diagonal rainbow stripes
--theme skittles      # Randomized rainbow colors per character
```

**Visual Effects:**
```bash
--arcs curved         # Bézier curve attack trails
--arcs straight       # Direct attack paths
--lighting            # Enable 3D globe shading
--light-follow        # Light rotates opposite to globe
--rain                # Matrix rain effect
--rain-density 5      # Rain density (0-10)
--protocol-glyphs     # Show attack type icons (# = SSH, ~ = Telnet, @ = SMTP, : = HTTP, % = FTP)
--crt                 # Retro CRT scanline effect
--glow 2              # Phosphor glow level (0-3)
```

**Demo Mode:**
```bash
--demo-storm          # Generate fake attack traffic (perfect for demos!)
--demo-rate 50        # Attacks per second (default: 10)
```

## ⚙️ Other Command Line Options

**Display Settings:**
- `-s <seconds>` - Globe rotation period (10-300, default: 30)
- `-r <milliseconds>` - Refresh rate (50-1000, default: 100)
- `-a <ratio>` - Character aspect ratio (1.0-4.0, default: 2.0)

**API Settings:**
- `-u <url>` - SecKC API base URL (default: https://mhn.h-i-r.net/seckcapi)
- `-e <count>` - Max events per API call (1-500, default: 50)
- `-p <duration>` - API polling interval (1s-300s, default: 2s)

**Configuration & Recording:**
- `--config <file>` - Load settings from TOML config file
- `--record <file>` - Record session to asciinema file
- `-d <filename>` - Enable debug logging

## 💡 Example Commands

```bash
# High-resolution Braille rendering with demo traffic
go run SecKC-MHN-Globe-Enhanced.go --charset braille --demo-storm

# Full visual effects showcase
go run SecKC-MHN-Globe-Enhanced.go --theme matrix --charset braille --rain --arcs curved --lighting --light-follow --protocol-glyphs --demo-storm --demo-rate 50

# Conference presentation mode with recording
go run SecKC-MHN-Globe-Enhanced.go --theme dracula --arcs curved --demo-storm --demo-rate 100 --record conference-demo.cast

# Live monitoring with Nord theme
go run SecKC-MHN-Globe-Enhanced.go --theme nord --arcs curved --lighting

# Retro CRT mode
go run SecKC-MHN-Globe-Enhanced.go --theme amber --charset blocks --crt --glow 2

# Original simple version
go run SecKC-MHN-Globe.go
```

## TOML Configuration File

You can save your preferred settings in a config file (e.g., `~/.config/seckc-globe.toml`):

```toml
[api]
base_url = "https://mhn.h-i-r.net/seckcapi"
poll_interval = "2s"
max_events = 50

[display]
theme = "matrix"
charset = "braille"
rotation_period = 30
refresh_rate = 100
aspect_ratio = 2.0

[effects]
arc_style = "curved"
trail_ms = 1200
rain_enabled = true
rain_density = 5
lighting_enabled = true
light_follow = true
protocol_glyphs = true
```

Load with: `./SecKC-MHN-Globe-Enhanced --config ~/.config/seckc-globe.toml`

**Note:** This program interfaces with the Public SecKC MHN Dashboard by default when no configuration is provided.

## Dependencies

```go
require (
    github.com/gdamore/tcell/v2 v2.8.1      // Terminal UI
    github.com/BurntSushi/toml v1.4.0       // TOML config parser
)
```

## License

This project is licensed under the BSD 2-Clause License - see the [LICENSE](LICENSE) file for details.

## Troubleshooting

### Common Issues

1. **Text cut off on the right side**: Use the **horizontal scrolling feature**! Press `.` to scroll right and see the rest of the text. Press `,` to scroll back left. Press `H` to reset scroll. All organization names and rDNS info are fully displayed - just scroll to see them!

2. **Command guide showing at bottom**: Press `C` to toggle the onscreen command guide on/off.

3. **Panels won't close**: Make sure to press the same key to toggle panels off:
   - `I` - Toggles attack info panel
   - `S` - Toggles top attackers stats panel
   - `P` - Toggles top IPs panel
   - `C` - Toggles command guide
   - `?` - Toggles help panel

4. **Rainbow/Skittles theme not showing**: Press `T` key to cycle through all 9 themes:
   - Rainbow (8th theme) = solid diagonal rainbow stripes
   - Skittles (9th theme) = randomized rainbow colors
   - Or start directly with `--theme rainbow` or `--theme skittles`

5. **Terminal display issues**: Ensure terminal supports color and proper size (minimum 80x24, recommended 200x50+)

6. **Performance problems**: Enable debug logging with `-d` option

7. **Globe landmass seems "blocky"**: Reduce terminal size to below 190x70 or try different character sets with `--charset braille`

8. **Window resize breaks display**: The program handles resizing dynamically. The globe uses 60% of window width, dashboard uses 40%. Expand the window to see more text without scrolling!

### Debug Mode

Enable debug logging:
```bash
./SecKC-MHN-Globe -d debug.log
```

This logs:
- Screen updates and rendering
- HPFeeds message processing
- GeoIP lookup results
- Dashboard updates
