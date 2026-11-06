package oracle

import "sort"

// Minimize shrinks a failing history to a smaller one that still fails, so
// a human can see the essence of the violation. First it projects the
// history onto the keys named by the violation (a projection of a legal
// history is legal, so a failing projection is itself a genuine violation),
// then it drops requests one at a time while the check keeps failing.
// maxChecks bounds the number of Check calls. The result is a debugging aid;
// the verdict on the original history is the authoritative one.
func Minimize(base, final State, ops []*Op, v *Violation, lim Limits, maxChecks int) (State, State, []*Op) {
	keys := map[string]bool{}
	if v != nil && v.Key != "" {
		keys[v.Key] = true
	}
	if v != nil {
		ids := map[string]bool{}
		for _, id := range v.OpIDs {
			ids[id] = true
		}
		for _, o := range ops {
			if !ids[o.ID] {
				continue
			}
			for k := range o.Effects() {
				if len(keys) < 64 {
					keys[k] = true
				}
			}
			if o.Kind == KGet {
				keys[o.Key] = true
			}
		}
	}
	checks := 0
	// A reduction is kept only if it still fails with the same kind of
	// violation, so the minimized history shows the original problem.
	fails := func(b, f State, h []*Op) bool {
		checks++
		r := Check(b, f, h, lim)
		return r.Result == Fail && (v == nil || (r.Violation != nil && r.Violation.Kind == v.Kind))
	}
	b, f, h := base, final, ops
	single := false
	if v != nil && v.Key != "" {
		pb, pf, ph := project(base, final, ops, map[string]bool{v.Key: true})
		if fails(pb, pf, ph) {
			b, f, h, single = pb, pf, ph, true
		}
	}
	if !single && len(keys) > 0 {
		pb, pf, ph := project(base, final, ops, keys)
		if fails(pb, pf, ph) {
			b, f, h = pb, pf, ph
		}
	}
	// Greedy removal, newest first; stop at maxChecks.
	for i := len(h) - 1; i >= 0 && checks < maxChecks; i-- {
		cand := make([]*Op, 0, len(h)-1)
		cand = append(cand, h[:i]...)
		cand = append(cand, h[i+1:]...)
		if fails(b, f, cand) {
			h = cand
		}
	}
	return b, f, h
}

func project(base, final State, ops []*Op, keys map[string]bool) (State, State, []*Op) {
	pb, pf := State{}, State{}
	for k := range keys {
		if v, ok := base[k]; ok {
			pb[k] = v
		}
		if v, ok := final[k]; ok {
			pf[k] = v
		}
	}
	var out []*Op
	for _, o := range ops {
		c := *o
		c.eff = nil
		if o.Kind == KGet {
			if keys[o.Key] {
				out = append(out, &c)
			}
			continue
		}
		c.Muts = nil
		for _, m := range o.Muts {
			if keys[m.Key] {
				c.Muts = append(c.Muts, m)
			}
		}
		if len(c.Muts) > 0 {
			out = append(out, &c)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Start < out[j].Start })
	return pb, pf, out
}
