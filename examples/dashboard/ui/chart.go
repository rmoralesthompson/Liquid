package ui

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	liquid "github.com/rmoralesthompson/liquid/core"
)

// chart viewBox geometry. Data is mapped into this fixed coordinate space and
// the SVG is stretched to the card width with preserveAspectRatio="none".
const (
	chartW    = 640
	chartH    = 150
	chartPadX = 6
	chartPadT = 14
	chartPadB = 12
)

// Chart is the time-series card: it plots the shared portfolio-value series as
// an inline SVG sparkline, re-rendered over SSE on every emission. Like the
// ticker it holds no per-instance state — every accessor reads the current
// window off the injected subject, so the first render and each pushed
// re-render draw from the same source. This is the answer to "can Liquid draw
// charts?": yes, as server-rendered SVG — no client charting library, and the
// live redraw rides the same D3 push path as every other card.
type Chart struct {
	// HydroID marks the component interactive so pushes can target it.
	HydroID string
	// Series is the app-lifetime rolling window of portfolio values.
	Series *liquid.BehaviorSubject[[]float64] `inject:""`
}

// Selector returns the custom element tag for the component.
func (c *Chart) Selector() string { return "app-chart" }

// Subscriptions follows the series subject: each emission re-renders the chart
// (the render recomputes the SVG from the latest window), so apply is a no-op.
func (c *Chart) Subscriptions() []liquid.Subscription {
	return []liquid.Subscription{
		liquid.Observe(c.Series, func([]float64) {}),
	}
}

// points maps the current series into viewBox coordinates.
func (c *Chart) points() []struct{ X, Y float64 } {
	data := c.Series.Value()
	pts := make([]struct{ X, Y float64 }, len(data))
	if len(data) == 0 {
		return pts
	}
	lo, hi := data[0], data[0]
	for _, v := range data {
		lo, hi = math.Min(lo, v), math.Max(hi, v)
	}
	span := hi - lo
	usableW := float64(chartW - 2*chartPadX)
	usableH := float64(chartH - chartPadT - chartPadB)
	for i, v := range data {
		x := chartPadX
		if len(data) > 1 {
			x = chartPadX + int(usableW*float64(i)/float64(len(data)-1))
		}
		// A flat series sits on the mid-line rather than snapping to an edge.
		frac := 0.5
		if span > 0 {
			frac = (v - lo) / span
		}
		y := float64(chartPadT) + usableH*(1-frac)
		pts[i] = struct{ X, Y float64 }{X: float64(x), Y: y}
	}
	return pts
}

// Line is the polyline points attribute: "x0,y0 x1,y1 …".
func (c *Chart) Line() string {
	pts := c.points()
	var b strings.Builder
	for i, p := range pts {
		if i > 0 {
			b.WriteByte(' ')
		}
		fmt.Fprintf(&b, "%.1f,%.1f", p.X, p.Y)
	}
	return b.String()
}

// Area is the filled-region path: the line, closed down to the baseline.
func (c *Chart) Area() string {
	pts := c.points()
	if len(pts) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "M%.1f,%.1f", pts[0].X, pts[0].Y)
	for _, p := range pts[1:] {
		fmt.Fprintf(&b, " L%.1f,%.1f", p.X, p.Y)
	}
	fmt.Fprintf(&b, " L%.1f,%d L%.1f,%d Z", pts[len(pts)-1].X, chartH-chartPadB, pts[0].X, chartH-chartPadB)
	return b.String()
}

// DotX and DotY position the marker at the latest point.
func (c *Chart) DotX() string { return c.lastCoord(true) }
func (c *Chart) DotY() string { return c.lastCoord(false) }

func (c *Chart) lastCoord(x bool) string {
	pts := c.points()
	if len(pts) == 0 {
		return "0"
	}
	last := pts[len(pts)-1]
	if x {
		return strconv.FormatFloat(last.X, 'f', 1, 64)
	}
	return strconv.FormatFloat(last.Y, 'f', 1, 64)
}

// Last is the current value, formatted as USD.
func (c *Chart) Last() string {
	data := c.Series.Value()
	if len(data) == 0 {
		return usd(0)
	}
	return usd(data[len(data)-1])
}

// Delta is the change from the first to the last point in the window.
func (c *Chart) Delta() string {
	data := c.Series.Value()
	if len(data) < 2 || data[0] == 0 {
		return pct(0)
	}
	return pct((data[len(data)-1] - data[0]) / data[0] * 100)
}

// Dir is "up" or "down" over the window, driving the delta's colour class.
func (c *Chart) Dir() string {
	data := c.Series.Value()
	if len(data) >= 2 && data[len(data)-1] < data[0] {
		return "down"
	}
	return "up"
}

// usd formats a value as "$127,815.20".
func usd(v float64) string {
	neg := v < 0
	if neg {
		v = -v
	}
	whole := int64(v)
	cents := int64(math.Round((v - float64(whole)) * 100))
	if cents >= 100 {
		whole++
		cents -= 100
	}
	s := "$" + group(whole) + "." + fmt.Sprintf("%02d", cents)
	if neg {
		s = "-" + s
	}
	return s
}

// pct formats a signed percentage: "+1.24%", "-0.80%".
func pct(v float64) string { return fmt.Sprintf("%+.2f%%", v) }

// group inserts thousands separators into a non-negative integer.
func group(n int64) string {
	digits := strconv.FormatInt(n, 10)
	if len(digits) <= 3 {
		return digits
	}
	var b strings.Builder
	head := len(digits) % 3
	if head > 0 {
		b.WriteString(digits[:head])
	}
	for i := head; i < len(digits); i += 3 {
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		b.WriteString(digits[i : i+3])
	}
	return b.String()
}
