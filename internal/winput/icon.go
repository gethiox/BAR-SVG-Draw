package winput

import "math"

// IconPixels renders the tray icon (rounded square with a pen squiggle) as
// top-down BGRA with straight alpha, 4×4 supersampled.
func IconPixels(size int, bg, ink [3]uint8) []byte {
	pix := make([]byte, size*size*4)
	n := float64(size)
	radius := n * 0.24
	thick := n * 0.11
	var pts [][2]float64
	for i := 0; i <= 40; i++ {
		u := float64(i) / 40
		pts = append(pts, [2]float64{n*0.2 + u*n*0.6, n*0.52 + math.Sin(u*2*math.Pi*1.25)*n*0.18})
	}
	inRounded := func(x, y float64) bool {
		cx := math.Max(radius, math.Min(n-radius, x))
		cy := math.Max(radius, math.Min(n-radius, y))
		dx, dy := x-cx, y-cy
		return x >= 0 && y >= 0 && x <= n && y <= n && dx*dx+dy*dy <= radius*radius
	}
	onInk := func(x, y float64) bool {
		for i := 1; i < len(pts); i++ {
			ax, ay, bx, by := pts[i-1][0], pts[i-1][1], pts[i][0], pts[i][1]
			vx, vy := bx-ax, by-ay
			t := math.Max(0, math.Min(1, ((x-ax)*vx+(y-ay)*vy)/(vx*vx+vy*vy)))
			px, py := ax+t*vx-x, ay+t*vy-y
			if px*px+py*py <= thick*thick/4 {
				return true
			}
		}
		return false
	}
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			var cover, inkCover float64
			for sy := 0; sy < 4; sy++ {
				for sx := 0; sx < 4; sx++ {
					fx, fy := float64(x)+(float64(sx)+0.5)/4, float64(y)+(float64(sy)+0.5)/4
					if inRounded(fx, fy) {
						cover++
						if onInk(fx, fy) {
							inkCover++
						}
					}
				}
			}
			if cover == 0 {
				continue
			}
			k := inkCover / cover
			i := (y*size + x) * 4
			for c := 0; c < 3; c++ {
				pix[i+c] = uint8(float64(bg[2-c])*(1-k) + float64(ink[2-c])*k + 0.5)
			}
			pix[i+3] = uint8(cover/16*255 + 0.5)
		}
	}
	return pix
}
