# Utils Directory
This directory contains utilities and assets for the SecKC-MHN-Globe project.

## Files

### `equirectangle_projection.png`

This PNG file contains an equirectangular projection of Earth used for realistic globe rendering in the terminal application.

**Source Attribution:**
- **Immediate Source**: https://github.com/n0xa/seckc-encom-boardroom/blob/master/images/equirectangle_projection.png
- **Previous Source**: arscan/encom-boardroom project
- **Original Source**: Unknown

### `convert_png.go`

A conversion utility created to transform the PNG map into an ASCII bitmap suitable for terminal rendering.

**Purpose:**
- Converts PNG geographic data to ASCII characters
- Enables realistic Earth visualization in terminal environments
- Provides land/water differentiation for the 3D globe renderer
- Optimized for character-based display with proper aspect ratio handling

**Usage:**
`go run convert_png.go` 
The output was embedded directly in the main application as the `getEarthBitmap()` function
