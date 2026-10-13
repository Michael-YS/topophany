package tui

import (
	"math"
	"sort"
)

// The network view: nodes stand in columns by depth along declared links and
// hosting, each as near to the mean height of its neighbours as spacing
// allows, so most edges run close to straight. The layout ignores focus and
// path hops, so moving the selection never rearranges the graph.

// layers assigns longest-path depth; cycles are broken at DFS back edges so
// any declared topology can be drawn.
func layers(nodes []mapNode, edges []mapEdge) map[string]int {
	adj := map[string][]string{}
	deg := map[string]int{}
	for _, e := range edges {
		adj[e.From] = append(adj[e.From], e.To)
		deg[e.From]++
		deg[e.To]++
	}
	state := map[string]int{}
	order := []string{}
	back := map[[2]string]bool{}
	type frame struct {
		id   string
		next int
	}
	for _, n := range nodes {
		if state[n.ID] != 0 || deg[n.ID] == 0 {
			continue
		}
		stack := []frame{{id: n.ID}}
		state[n.ID] = 1
		for len(stack) > 0 {
			f := &stack[len(stack)-1]
			if f.next == len(adj[f.id]) {
				state[f.id] = 2
				order = append(order, f.id)
				stack = stack[:len(stack)-1]
				continue
			}
			u := adj[f.id][f.next]
			f.next++
			switch state[u] {
			case 0:
				state[u] = 1
				stack = append(stack, frame{id: u})
			case 1:
				back[[2]string{f.id, u}] = true
			}
		}
	}
	layer := map[string]int{}
	maxL := 0
	for i := len(order) - 1; i >= 0; i-- {
		v := order[i]
		for _, u := range adj[v] {
			if !back[[2]string{v, u}] && layer[v]+1 > layer[u] {
				layer[u] = min(32, layer[v]+1)
			}
		}
	}
	for _, n := range nodes {
		maxL = max(maxL, layer[n.ID])
	}
	for _, n := range nodes {
		if deg[n.ID] == 0 {
			layer[n.ID] = maxL
		}
	}
	return layer
}

// layoutEdges are the edges that shape the layout: declared links and
// hosting, plus path hops only for nodes that have nothing else.
func layoutEdges(nodes []mapNode, edges []mapEdge) []mapEdge {
	base := []mapEdge{}
	linked := map[string]bool{}
	for _, e := range edges {
		if e.Kind != edgePath {
			base = append(base, e)
			linked[e.From], linked[e.To] = true, true
		}
	}
	for _, e := range edges {
		if e.Kind == edgePath && (!linked[e.From] || !linked[e.To]) {
			base = append(base, e)
		}
	}
	return base
}

// packRows keeps sorted rows apart by at least gaps[k] (the gap before row
// k) inside [lo, hi], moving each as little as possible from where it wants
// to be. Gaps shrink evenly when they cannot all fit.
func packRows(ws, gaps []float64, lo, hi float64) {
	n := len(ws)
	if n == 0 {
		return
	}
	need := 0.0
	for k := 1; k < n; k++ {
		need += gaps[k]
	}
	if need > hi-lo && need > 0 {
		for k := range gaps {
			gaps[k] *= (hi - lo) / need
		}
	}
	for k := range ws {
		ws[k] = clamp(ws[k], lo, hi)
	}
	for k := 1; k < n; k++ {
		ws[k] = math.Max(ws[k], ws[k-1]+gaps[k])
	}
	if ws[n-1] > hi {
		ws[n-1] = hi
		for k := n - 2; k >= 0; k-- {
			ws[k] = math.Min(ws[k], ws[k+1]-gaps[k+1])
		}
	}
}

func (m *Model) drawNetwork(cv *Canvas, x, y, w, h int, nodes []mapNode, edges []mapEdge, groups map[string]Color, f mapFocus) {
	if len(nodes) == 0 {
		msg := "Empty topology — press a to add a node"
		cv.Text(x+max(0, (w-width(msg))/2), y+h/2, msg, fg(cMuted).italic(), w)
		return
	}
	shape := layoutEdges(nodes, edges)
	layer := layers(nodes, shape)
	nl := 0
	for _, l := range layer {
		nl = max(nl, l+1)
	}
	idx := map[string]int{}
	for i, n := range nodes {
		idx[n.ID] = i
	}
	// Vertices are the nodes plus one waypoint in every column a long edge
	// crosses, so long edges are routed around chips instead of through them.
	vlayer := make([]int, len(nodes))
	for i, n := range nodes {
		vlayer[i] = layer[n.ID]
	}
	chains := map[[2]string][]int{}
	pairs := [][2]int{}
	for _, e := range shape {
		a, b := idx[e.From], idx[e.To]
		if vlayer[b] <= vlayer[a] || chains[[2]string{e.From, e.To}] != nil {
			continue
		}
		c := []int{a}
		// Cap optional routing waypoints independently of graph size.
		for l := vlayer[a] + 1; l < vlayer[b] && len(vlayer) < 8192; l++ {
			vlayer = append(vlayer, l)
			c = append(c, len(vlayer)-1)
		}
		c = append(c, b)
		for k := 1; k < len(c); k++ {
			pairs = append(pairs, [2]int{c[k-1], c[k]})
		}
		chains[[2]string{e.From, e.To}] = c
	}
	nv := len(vlayer)
	isNode := func(v int) bool { return v < len(nodes) }
	cols := make([][]int, nl)
	for v := 0; v < nv; v++ {
		cols[vlayer[v]] = append(cols[vlayer[v]], v)
	}
	nb := make([][]int, nv)
	for _, p := range pairs {
		nb[p[0]] = append(nb[p[0]], p[1])
		nb[p[1]] = append(nb[p[1]], p[0])
	}
	// Rows start evenly spread, then each vertex is pulled toward the mean
	// row of its neighbours, sweeping right and left. Chips keep a blank row
	// between them; waypoints need only their own row.
	top, bottom := 0.0, float64(h-1)
	pos := make([]float64, nv)
	for _, c := range cols {
		for r, v := range c {
			pos[v] = (float64(r)+.5)*float64(h)/float64(len(c)) - .5
		}
	}
	gapsOf := func(c []int) []float64 {
		g := make([]float64, len(c))
		for k := 1; k < len(c); k++ {
			g[k] = 1
			if isNode(c[k]) && isNode(c[k-1]) {
				g[k] = 2
			}
		}
		return g
	}
	for iter := 0; iter < 16; iter++ {
		for k := range cols {
			ci := k
			if iter%2 == 1 {
				ci = len(cols) - 1 - k
			}
			c := cols[ci]
			want := map[int]float64{}
			for _, v := range c {
				want[v] = pos[v]
				if len(nb[v]) > 0 {
					sum := 0.0
					for _, u := range nb[v] {
						sum += pos[u]
					}
					want[v] = sum / float64(len(nb[v]))
				}
			}
			sort.SliceStable(c, func(a, b int) bool { return want[c[a]] < want[c[b]] })
			ws := make([]float64, len(c))
			for k, v := range c {
				ws[k] = want[v]
			}
			packRows(ws, gapsOf(c), top, bottom)
			for k, v := range c {
				pos[v] = ws[k]
			}
		}
	}
	// Averaging pulls the whole graph together; spread it back out, at most
	// six rows per chip of the busiest column, centred.
	lo, hi, busiest := math.Inf(1), math.Inf(-1), 1.0
	for _, c := range cols {
		n := 0.0
		for _, v := range c {
			if isNode(v) {
				n++
			} else {
				n += .5
			}
		}
		busiest = math.Max(busiest, n)
	}
	for _, p := range pos {
		lo, hi = math.Min(lo, p), math.Max(hi, p)
	}
	if span := hi - lo; span > 0 {
		target := math.Min(bottom-top, math.Max(span, busiest*6))
		off := top + (bottom-top-target)/2
		for i := range pos {
			pos[i] = off + (pos[i]-lo)*target/span
		}
	} else {
		for i := range pos {
			pos[i] = (top + bottom) / 2
		}
	}
	row := make([]int, nv)
	for _, c := range cols {
		prev := -1
		for _, v := range c {
			r := max(int(math.Round(pos[v])), prev+1)
			row[v] = min(r, h-1)
			prev = row[v]
		}
	}
	// Chips show the node name, or its (usually short) ID when the name does
	// not fit. Each column is as wide as its widest chip; what is left becomes
	// equal gaps for the edges.
	label := make([]string, len(nodes))
	for i, n := range nodes {
		label[i] = n.Name
	}
	colW := make([]int, nl)
	sizeCols := func() int {
		total := 0
		for l, c := range cols {
			colW[l] = 2
			for _, v := range c {
				if isNode(v) {
					colW[l] = max(colW[l], width(label[v])+4)
				}
			}
			total += colW[l]
		}
		return total
	}
	gapW := 4
	// Names that would lose more than a fifth of the room go to IDs.
	if (sizeCols()+(nl-1)*gapW)*4 > (w-2)*5 {
		for i, n := range nodes {
			if width(n.ID) < width(n.Name) {
				label[i] = n.ID
			}
		}
		sizeCols()
	}
	for {
		total := (nl - 1) * gapW
		widest := 0
		for l, cw := range colW {
			total += cw
			if cw > colW[widest] {
				widest = l
			}
		}
		if total <= w-2 || colW[widest] <= 8 {
			if nl > 1 && total < w-2 {
				gapW += (w - 2 - total) / (nl - 1)
			} else if nl > 1 && total > w-2 {
				gapW = max(1, gapW-(total-w+2+nl-2)/(nl-1))
			}
			break
		}
		colW[widest]--
	}
	colX := make([]int, nl)
	for l, cx := 0, 1; l < nl; l++ {
		colX[l] = cx
		cx += colW[l] + gapW
	}
	type box struct{ x, y, w int }
	boxes := make([]box, len(nodes))
	for i := range nodes {
		l := vlayer[i]
		boxes[i] = box{x: colX[l], y: row[i], w: max(4, min(width(label[i])+4, colW[l], w-colX[l]))}
	}
	// Path hops are overlays: drawn when they fail, belong to the observer
	// in view or to the selected route, never used to place nodes.
	drawn := []mapEdge{}
	for _, e := range edges {
		route := f.hops[[2]string{e.From, e.To}] || f.hops[[2]string{e.To, e.From}]
		if e.Kind == edgePath && !route && signalRank(e.Signal) < 3 && (m.Observer == "" || e.Observer != m.Observer) {
			continue
		}
		drawn = append(drawn, e)
	}
	d := NewDots(w, h)
	phase := m.phase()
	type arrow struct {
		x, y int
		c    Color
	}
	arrows := []arrow{}
	dot := func(cx, cy int) [2]float64 { return [2]float64{float64(cx * 2), float64(cy*4 + 2)} }
	for _, e := range drawn {
		a, b := boxes[idx[e.From]], boxes[idx[e.To]]
		c, pri, dash := edgeStyle(e, f)
		var path [][2]float64
		p0 := dot(a.x+a.w, a.y)
		p0[0]++
		p3 := dot(b.x-1, b.y)
		p3[0]--
		chain := chains[[2]string{e.From, e.To}]
		switch {
		case layer[e.From] == layer[e.To] && e.Kind == edgeContain:
			// Stacked in one column: a bracket on the left margin.
			q0 := [2]float64{float64(a.x*2 - 1), p0[1]}
			q3 := [2]float64{float64(b.x*2 - 1), p3[1]}
			path = curve(q0, [2]float64{q0[0] - 4, q0[1]}, [2]float64{q3[0] - 4, q3[1]}, q3)
		case layer[e.To] <= layer[e.From]:
			// Back edge: loop around the right side of both chips, dashed.
			p3 = [2]float64{float64((b.x+b.w)*2 + 1), p3[1]}
			bend := math.Max(p0[0], p3[0]) + 8
			path = curve(p0, [2]float64{bend, p0[1]}, [2]float64{bend, p3[1]}, p3)
			dash = max(dash, 2)
		default:
			// Through each waypoint: straight across its column, eased
			// between columns with horizontal tangents.
			from := p0
			hop := func(to [2]float64) {
				tan := math.Max((to[0]-from[0])*.45, 2)
				path = append(path, curve(from, [2]float64{from[0] + tan, from[1]}, [2]float64{to[0] - tan, to[1]}, to)...)
			}
			for k := 1; k+1 < len(chain); k++ {
				v := chain[k]
				l := vlayer[v]
				in, out := dot(colX[l], row[v]), dot(colX[l]+colW[l], row[v])
				hop(in)
				for px := in[0]; px <= out[0]; px++ {
					path = append(path, [2]float64{px, in[1]})
				}
				from = out
			}
			hop(p3)
			if e.Kind != edgeContain {
				arrows = append(arrows, arrow{b.x - 1, b.y, c})
			}
		}
		d.Polyline(path, c, pri, dash)
		m.pulse(d, path, e, phase, false)
	}
	d.Blit(cv, x, y, 0)
	for _, a := range arrows {
		if a.x >= 0 && a.x < w {
			cv.Set(x+a.x, y+a.y, '▸', fg(a.c))
		}
	}
	for i, n := range nodes {
		b := boxes[i]
		if b.x+b.w > w {
			continue
		}
		gc := groups[n.Group]
		chip := Mix(gc, cInk, .86)
		st := fg(Mix(gc, cText, .3)).Bg(chip)
		if n.Kind == "external" {
			st = st.italic()
		}
		if f.hasNode(n.ID) {
			chip = cSelBG
			st = fg(cGlow).Bg(chip).bold()
		}
		sig := nodeSignal(n)
		cv.Set(x+b.x, y+b.y, ' ', Style{BG: chip})
		cv.Set(x+b.x+1, y+b.y, signalGlyph(sig), fg(signalColor(sig)).Bg(chip).bold())
		cv.Pad(x+b.x+2, y+b.y, " "+ellipsis(label[i], b.w-4)+" ", st, b.w-2)
		m.hit(x+b.x, y+b.y, b.w, "select:node:"+n.ID)
	}
	tag := " network "
	cv.Text(x+w-width(tag)-1, y, tag, fg(cFaint), -1)
}
