// Package media 把上传的图片处理成站点需要的一组派生文件。
//
// 只做一件事：解码原图，按固定宽度生成 webp。视频、文档、动图一律不动，
// 由调用方原样保存。编码走 gen2brain/webp 的 wasm 后端——纯 Go，
// 不需要 cgo，交叉编译和在低配机器上部署都不受影响。
package media

import (
	"bytes"
	"errors"
	"image"
	"math"
	"strconv"

	// 注册标准库支持的解码器。
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	"github.com/gen2brain/webp"
	"golang.org/x/image/draw"
)

// TargetWidths 是站点需要的三档宽度，从大到小。
var TargetWidths = []int{1600, 960, 480}

const (
	// 有损质量。82 在照片上看不出损失，体积约为无损的三分之一。
	quality = 82
	// 编码速度/质量折中，4 是库的默认值。
	method = 4
	// 解码上限：四千万像素。超过这个数说明不是正常上传，
	// 让它在解码前就被拦下，避免一张图把内存打满。
	maxPixels = 40_000_000
)

// ErrUndecodable 表示内容看起来是图片，但解不出来。
var ErrUndecodable = errors.New("图片无法解析")

// ErrTooLarge 表示像素数超过上限。
var ErrTooLarge = errors.New("图片尺寸超出上限")

// Rendition 是一档派生图。
type Rendition struct {
	// Name 是变体名，如 "w480"。
	Name   string
	Width  int
	Height int
	Data   []byte
}

// Result 是处理结果。原图不在这里——调用方自己保存原文件。
type Result struct {
	Width      int
	Height     int
	Renditions []Rendition
}

// Processable 判断这类内容要不要走图片管线。
//
// gif 被排除：站内动画 gif 转成静态 webp 会把动画弄丢，
// 宁可原样保存，也不要静默损坏内容。
func Processable(contentType string) bool {
	switch contentType {
	case "image/jpeg", "image/png", "image/webp":
		return true
	default:
		return false
	}
}

// Process 解码原图并生成各档派生图。原图宽度小于目标宽度时跳过该档。
func Process(data []byte) (*Result, error) {
	width, height, err := dimensions(data)
	if err != nil {
		return nil, err
	}
	if int64(width)*int64(height) > maxPixels {
		return nil, ErrTooLarge
	}

	source, err := decode(data)
	if err != nil {
		return nil, ErrUndecodable
	}

	result := &Result{Width: width, Height: height}
	for _, target := range TargetWidths {
		if target >= width {
			continue
		}
		resized := scale(source, target)
		encoded, err := encode(resized)
		if err != nil {
			return nil, err
		}
		bounds := resized.Bounds()
		result.Renditions = append(result.Renditions, Rendition{
			Name:   "w" + strconv.Itoa(target),
			Width:  bounds.Dx(),
			Height: bounds.Dy(),
			Data:   encoded,
		})
	}
	return result, nil
}

// dimensions 只读文件头，不解码整张图。
func dimensions(data []byte) (int, int, error) {
	if cfg, _, err := image.DecodeConfig(bytes.NewReader(data)); err == nil {
		return cfg.Width, cfg.Height, nil
	}
	if cfg, err := webp.DecodeConfig(bytes.NewReader(data)); err == nil {
		return cfg.Width, cfg.Height, nil
	}
	return 0, 0, ErrUndecodable
}

func decode(data []byte) (image.Image, error) {
	if img, _, err := image.Decode(bytes.NewReader(data)); err == nil {
		return img, nil
	}
	return webp.Decode(bytes.NewReader(data))
}

// scale 等比缩放到指定宽度，用 CatmullRom 保证缩小后不糊。
func scale(source image.Image, width int) image.Image {
	bounds := source.Bounds()
	height := int(math.Round(float64(bounds.Dy()) * float64(width) / float64(bounds.Dx())))
	if height < 1 {
		height = 1
	}
	target := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.CatmullRom.Scale(target, target.Bounds(), source, bounds, draw.Over, nil)
	return target
}

func encode(img image.Image) ([]byte, error) {
	var buffer bytes.Buffer
	if err := webp.Encode(&buffer, img, webp.Options{Quality: quality, Method: method}); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}
