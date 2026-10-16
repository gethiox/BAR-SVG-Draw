package raster

import (
	"math"

	"bardraw/internal/geom"
)

type gnode struct {
	pos geom.Pt
	deg int // graph degree (edge incidences)
}

type gedge struct {
	a, b int // node ids; b == -1 for dangling ends
	pts  []geom.Pt
	dead bool
}

// Trace converts a thinned mask into polylines (raster coordinates).
// Short spurs (< spurLen px) hanging off junctions are pruned and edges are
// chained through junctions, preferring the straightest continuation, so each
// returned polyline can be drawn as a single pen-down stroke.
func (m *Mask) Trace(spurLen float64) []geom.Polyline {
	w := m.W
	nodeOf := make([]int32, len(m.B))
	for i := range nodeOf {
		nodeOf[i] = -1
	}
	deg := make([]uint8, len(m.B))
	var ink []int
	for i, v := range m.B {
		if v != 0 {
			ink = append(ink, i)
			deg[i] = uint8(popcount8(m.nmask(i)))
		}
	}
	pix := func(i int) geom.Pt { return geom.Pt{X: float64(i%w) + 0.5, Y: float64(i/w) + 0.5} }
	nbr := func(i, k int) int { return i + ndy[k]*w + ndx[k] }

	var nodes []gnode
	// junction clusters
	for _, i := range ink {
		if deg[i] < 3 || nodeOf[i] >= 0 {
			continue
		}
		id := int32(len(nodes))
		stack := []int{i}
		nodeOf[i] = id
		sum := geom.Pt{}
		cnt := 0.0
		for len(stack) > 0 {
			p := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			sum = sum.Add(pix(p))
			cnt++
			for k := 0; k < 8; k++ {
				q := nbr(p, k)
				if m.B[q] != 0 && deg[q] >= 3 && nodeOf[q] < 0 {
					nodeOf[q] = id
					stack = append(stack, q)
				}
			}
		}
		nodes = append(nodes, gnode{pos: sum.Mul(1 / cnt)})
	}
	// endpoints
	for _, i := range ink {
		if deg[i] == 1 {
			nodeOf[i] = int32(len(nodes))
			nodes = append(nodes, gnode{pos: pix(i)})
		}
	}

	visited := make([]bool, len(m.B))
	var edges []gedge
	pairSeen := map[[2]int32]bool{}
	for _, p := range ink {
		np := nodeOf[p]
		if np < 0 {
			continue
		}
		for k := 0; k < 8; k++ {
			q := nbr(p, k)
			if m.B[q] == 0 {
				continue
			}
			if nq := nodeOf[q]; nq >= 0 {
				if nq == np {
					continue
				}
				key := [2]int32{np, nq}
				if np > nq {
					key = [2]int32{nq, np}
				}
				if !pairSeen[key] {
					pairSeen[key] = true
					edges = append(edges, gedge{a: int(np), b: int(nq), pts: []geom.Pt{nodes[np].pos, nodes[nq].pos}})
				}
				continue
			}
			if visited[q] {
				continue
			}
			pts := []geom.Pt{nodes[np].pos, pix(q)}
			visited[q] = true
			prev, cur := p, q
			end := -1
			for {
				next := -1
				for kk := 0; kk < 8; kk++ {
					c := nbr(cur, kk)
					if c != prev && m.B[c] != 0 {
						if nodeOf[c] >= 0 {
							next = c
							break
						}
						if !visited[c] && next < 0 {
							next = c
						}
					}
				}
				if next < 0 {
					break
				}
				if nodeOf[next] >= 0 {
					end = int(nodeOf[next])
					pts = append(pts, nodes[end].pos)
					break
				}
				visited[next] = true
				pts = append(pts, pix(next))
				prev, cur = cur, next
			}
			edges = append(edges, gedge{a: int(np), b: end, pts: pts})
		}
	}
	// closed loops without any node
	var loops [][]geom.Pt
	for _, s := range ink {
		if visited[s] || nodeOf[s] >= 0 || deg[s] != 2 {
			continue
		}
		visited[s] = true
		pts := []geom.Pt{pix(s)}
		prev, cur := -1, s
		for {
			next := -1
			for k := 0; k < 8; k++ {
				c := nbr(cur, k)
				if c != prev && m.B[c] != 0 && !visited[c] {
					next = c
					break
				}
			}
			if next < 0 {
				break
			}
			visited[next] = true
			pts = append(pts, pix(next))
			prev, cur = cur, next
		}
		if len(pts) >= 3 {
			loops = append(loops, pts)
		}
	}

	pruneSpurs(nodes, edges, spurLen)
	for i := range edges {
		if !edges[i].dead {
			edges[i].pts = smoothOpen(edges[i].pts, 2)
		}
	}
	out := chain(nodes, edges)
	for _, l := range loops {
		out = append(out, geom.Polyline{Pts: smoothClosed(l, 2), Closed: true})
	}
	return out
}

func recountDegrees(nodes []gnode, edges []gedge) {
	for i := range nodes {
		nodes[i].deg = 0
	}
	for _, e := range edges {
		if e.dead {
			continue
		}
		nodes[e.a].deg++
		if e.b >= 0 {
			nodes[e.b].deg++
		}
	}
}

// pruneSpurs removes short branches between an endpoint and a junction.
func pruneSpurs(nodes []gnode, edges []gedge, spurLen float64) {
	if spurLen <= 0 {
		return
	}
	for iter := 0; iter < 3; iter++ {
		recountDegrees(nodes, edges)
		removed := 0
		for i := range edges {
			e := &edges[i]
			if e.dead || e.b < 0 || e.a == e.b {
				continue
			}
			da, db := nodes[e.a].deg, nodes[e.b].deg
			if !((da == 1 && db >= 3) || (db == 1 && da >= 3)) {
				continue
			}
			if geom.PathLength(e.pts) < spurLen {
				e.dead = true
				removed++
			}
		}
		if removed == 0 {
			break
		}
	}
	recountDegrees(nodes, edges)
}

func smoothOpen(pts []geom.Pt, iters int) []geom.Pt {
	if len(pts) < 3 {
		return pts
	}
	cur := append([]geom.Pt(nil), pts...)
	tmp := make([]geom.Pt, len(pts))
	for it := 0; it < iters; it++ {
		tmp[0], tmp[len(cur)-1] = cur[0], cur[len(cur)-1]
		for i := 1; i < len(cur)-1; i++ {
			tmp[i] = cur[i-1].Add(cur[i].Mul(2)).Add(cur[i+1]).Mul(0.25)
		}
		cur, tmp = tmp, cur
	}
	return cur
}

func smoothClosed(pts []geom.Pt, iters int) []geom.Pt {
	n := len(pts)
	if n < 4 {
		return pts
	}
	cur := append([]geom.Pt(nil), pts...)
	tmp := make([]geom.Pt, n)
	for it := 0; it < iters; it++ {
		for i := 0; i < n; i++ {
			tmp[i] = cur[(i+n-1)%n].Add(cur[i].Mul(2)).Add(cur[(i+1)%n]).Mul(0.25)
		}
		cur, tmp = tmp, cur
	}
	return cur
}

// direction of a point sequence leaving its first point (~4 px look-ahead)
func leaveDir(pts []geom.Pt) geom.Pt {
	a := pts[0]
	for i := 1; i < len(pts); i++ {
		if pts[i].Dist(a) >= 4 || i == len(pts)-1 {
			d := pts[i].Sub(a)
			if l := d.Len(); l > 0 {
				return d.Mul(1 / l)
			}
		}
	}
	return geom.Pt{}
}

func reversed(p []geom.Pt) []geom.Pt {
	out := make([]geom.Pt, len(p))
	for i := range p {
		out[len(p)-1-i] = p[i]
	}
	return out
}

// chain walks the edge graph into long strokes (Euler-path style greedy).
func chain(nodes []gnode, edges []gedge) []geom.Polyline {
	type inc struct {
		e   int
		end int // 0: edge starts at this node, 1: edge ends here
	}
	adj := make([][]inc, len(nodes))
	rem := make([]int, len(nodes))
	for i, e := range edges {
		if e.dead {
			continue
		}
		adj[e.a] = append(adj[e.a], inc{i, 0})
		rem[e.a]++
		if e.b >= 0 {
			adj[e.b] = append(adj[e.b], inc{i, 1})
			rem[e.b]++
		}
	}
	used := make([]bool, len(edges))
	var out []geom.Polyline

	// dangling edges (b == -1) are walked from their node outward
	walk := func(start int) []geom.Pt {
		var path []geom.Pt
		cur := start
		var inDir geom.Pt
		for {
			best, bestEnd, bestScore := -1, 0, math.Inf(1)
			var bestPts []geom.Pt
			for _, in := range adj[cur] {
				if used[in.e] {
					continue
				}
				pts := edges[in.e].pts
				if in.end == 1 {
					pts = reversed(pts)
				}
				score := 0.0
				if inDir != (geom.Pt{}) {
					score = -inDir.Dot(leaveDir(pts)) // prefer straight continuation
				} else {
					score = -geom.PathLength(pts) // first step: longest branch
				}
				if score < bestScore {
					best, bestEnd, bestScore, bestPts = in.e, in.end, score, pts
				}
			}
			if best < 0 {
				return path
			}
			used[best] = true
			e := edges[best]
			rem[e.a]--
			if e.b >= 0 {
				rem[e.b]--
			}
			if len(path) == 0 {
				path = append(path, bestPts...)
			} else {
				path = append(path, bestPts[1:]...)
			}
			// next node is the far end of the edge
			nextNode := e.b
			if bestEnd == 1 {
				nextNode = e.a
			}
			if nextNode < 0 {
				return path
			}
			inDir = leaveDir(reversed(bestPts)).Mul(-1)
			cur = nextNode
		}
	}
	for {
		start := -1
		for i := range nodes { // odd remaining degree first (Euler path heuristic)
			if rem[i]%2 == 1 {
				start = i
				break
			}
		}
		if start < 0 {
			for i := range nodes {
				if rem[i] > 0 {
					start = i
					break
				}
			}
		}
		if start < 0 {
			break
		}
		path := walk(start)
		if len(path) >= 2 {
			out = append(out, geom.Polyline{Pts: path})
		} else if rem[start] > 0 {
			rem[start] = 0 // safety: avoid infinite loop on inconsistent data
		}
	}
	return out
}
