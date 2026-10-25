package ui

import "strings"

// ── Line diff ──────────────────────────────────────────────────────
//
// Review shows how vars.yml differs from the last successful deploy's.
// The files are a few hundred lines, so a plain LCS table is quick enough.

type diffLine struct {
	op   byte // ' ' same, '-' only in the old file, '+' only in the new
	text string
}

func lineDiff(old, cur []string) []diffLine {
	n, m := len(old), len(cur)
	// lcs[i][j]: longest common subsequence of old[i:] and cur[j:].
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if old[i] == cur[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	var out []diffLine
	i, j := 0, 0
	for i < n || j < m {
		switch {
		case i < n && j < m && old[i] == cur[j]:
			out = append(out, diffLine{' ', old[i]})
			i, j = i+1, j+1
		case i < n && (j == m || lcs[i+1][j] >= lcs[i][j+1]):
			out = append(out, diffLine{'-', old[i]})
			i++
		default:
			out = append(out, diffLine{'+', cur[j]})
			j++
		}
	}
	return out
}

// diffChanges counts the lines added and removed.
func diffChanges(d []diffLine) (added, removed int) {
	for _, l := range d {
		switch l.op {
		case '+':
			added++
		case '-':
			removed++
		}
	}
	return
}

// diffView renders the changed lines with ctx lines around each, and a
// faint ⋯ where unchanged lines are left out.
func diffView(d []diffLine, ctx int) string {
	keep := make([]bool, len(d))
	for i, l := range d {
		if l.op == ' ' {
			continue
		}
		for k := max(0, i-ctx); k <= min(len(d)-1, i+ctx); k++ {
			keep[k] = true
		}
	}
	var out []string
	skipped := false
	for i, l := range d {
		if !keep[i] {
			skipped = true
			continue
		}
		if skipped && len(out) > 0 {
			out = append(out, sFaint.Render("  ⋯"))
		}
		skipped = false
		l.text = maskLine(l.text)
		switch l.op {
		case '+':
			out = append(out, sGood.Render("+ "+l.text))
		case '-':
			out = append(out, sBad.Render("- "+l.text))
		default:
			out = append(out, sFaint.Render("  "+l.text))
		}
	}
	return strings.Join(out, "\n")
}
