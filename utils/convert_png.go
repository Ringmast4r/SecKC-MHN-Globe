package main

// Creates an ASCII bitmap from an equirectangle projection PNG of Earth to help render a globe.
// Original Projection borrowed from https://github.com/arscan/encom-globe

import (
	"fmt"
	"image/png"
	"os"
)

func main() {
	// Open the PNG file
	file, err := os.Open("equirectangle_projection.png")
	if err != nil {
		fmt.Printf("Error opening file: %v\n", err)
		return
	}
	defer file.Close()

	// Decode the PNG
	img, err := png.Decode(file)
	if err != nil {
		fmt.Printf("Error decoding PNG: %v\n", err)
		return
	}

	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()
	
	// Target ASCII dimensions (reasonable for terminal display)
	targetWidth := 120
	targetHeight := 60

	// Scale factors
	scaleX := float64(width) / float64(targetWidth)
	scaleY := float64(height) / float64(targetHeight)

	// Convert to ASCII
	fmt.Println("func getEarthBitmap() []string {")
	fmt.Println("\treturn []string{")

	for y := 0; y < targetHeight; y++ {
		fmt.Print("\t\t\"")
		for x := 0; x < targetWidth; x++ {
			imgX := int(float64(x) * scaleX)
			imgY := int(float64(y) * scaleY)

			// Get pixel color
			r, g, b, _ := img.At(imgX, imgY).RGBA()
			
			// Source image is B/W so keep it simple
			if ((r + g + b) / 3 > 128){
				// light areas are water: " "
				fmt.Print(" ")
			} else {
				// dark areas are land: "#"
				fmt.Print("#")
			}
		}
		fmt.Println("\",")
	}

	fmt.Println("\t}")
	fmt.Println("}")
}
