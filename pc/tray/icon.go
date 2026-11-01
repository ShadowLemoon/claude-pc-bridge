package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"math"
	"runtime"
)

var (
	colorGreen  = color.RGBA{0x2e, 0xa0, 0x43, 0xff}
	colorYellow = color.RGBA{0xd9, 0xa4, 0x06, 0xff}
	colorRed    = color.RGBA{0xcf, 0x22, 0x2e, 0xff}
	colorGray   = color.RGBA{0x8c, 0x95, 0x9f, 0xff}
)

// makeIcon 画一个实心圆点作为托盘图标。Windows 要 .ico 格式，其他系统用 .png
func makeIcon(c color.RGBA) []byte {
	const size = 32
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	center, radius := float64(size)/2, float64(size)/2-2
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			d := math.Hypot(float64(x)+0.5-center, float64(y)+0.5-center)
			// 边缘 1 像素做抗锯齿
			alpha := math.Max(0, math.Min(1, radius-d+0.5))
			if alpha > 0 {
				img.SetRGBA(x, y, color.RGBA{
					uint8(float64(c.R) * alpha), uint8(float64(c.G) * alpha),
					uint8(float64(c.B) * alpha), uint8(255 * alpha),
				})
			}
		}
	}
	var pngBuf bytes.Buffer
	_ = png.Encode(&pngBuf, img)
	if runtime.GOOS != "windows" {
		return pngBuf.Bytes()
	}
	// .ico 文件头 + 一个目录项，图像数据直接用 PNG（Windows Vista 起支持）
	var ico bytes.Buffer
	_ = binary.Write(&ico, binary.LittleEndian, []uint16{0, 1, 1})
	ico.Write([]byte{size, size, 0, 0})
	_ = binary.Write(&ico, binary.LittleEndian, []uint16{1, 32})
	_ = binary.Write(&ico, binary.LittleEndian, []uint32{uint32(pngBuf.Len()), 22})
	ico.Write(pngBuf.Bytes())
	return ico.Bytes()
}
