package service

import (
	"math"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/mozillazg/go-pinyin"
	"oneapp/internal/domain"
)

type indexedApp struct {
	app            domain.AppOverview
	lowerName      string
	pinyinFull     string
	pinyinInitials string
	lowerAliases   []string
	aliasPinyin    []string
	aliasInitials  []string
	lowerTags      []string
	lowerCat       string
	lowerDesc      string
}

// SearchService provides sub-5ms in-memory fuzzy, Chinese pinyin, alias, tag, and Frecency search.
type SearchService struct {
	mu      sync.RWMutex
	entries []indexedApp
	running map[string][]int
}

// NewSearchService initializes an empty search index.
func NewSearchService() *SearchService {
	return &SearchService{
		running: make(map[string][]int),
	}
}

// Rebuild refreshes the in-memory search index from the latest hydrated app list and running states.
func (s *SearchService) Rebuild(apps []domain.AppOverview, running map[string][]int) {
	items := make([]indexedApp, 0, len(apps))
	for _, a := range apps {
		full, init := computePinyin(a.Name)
		aliases := make([]string, 0, len(a.Aliases))
		aliasFull := make([]string, 0, len(a.Aliases))
		aliasInit := make([]string, 0, len(a.Aliases))
		for _, al := range a.Aliases {
			la := strings.ToLower(strings.TrimSpace(al))
			if la == "" {
				continue
			}
			aliases = append(aliases, la)
			af, ai := computePinyin(la)
			aliasFull = append(aliasFull, af)
			aliasInit = append(aliasInit, ai)
		}
		tags := make([]string, 0, len(a.Tags))
		for _, t := range a.Tags {
			lt := strings.ToLower(strings.TrimSpace(t))
			if lt != "" {
				tags = append(tags, lt)
			}
		}
		items = append(items, indexedApp{
			app:            a,
			lowerName:      strings.ToLower(a.Name),
			pinyinFull:     full,
			pinyinInitials: init,
			lowerAliases:   aliases,
			aliasPinyin:    aliasFull,
			aliasInitials:  aliasInit,
			lowerTags:      tags,
			lowerCat:       strings.ToLower(a.CategoryName),
			lowerDesc:      strings.ToLower(a.Description),
		})
	}

	s.mu.Lock()
	s.entries = items
	if running != nil {
		s.running = copyRunningMap(running)
	}
	s.mu.Unlock()
}

// UpdateRunning updates active PID mappings in the search index.
func (s *SearchService) UpdateRunning(running map[string][]int) {
	s.mu.Lock()
	s.running = copyRunningMap(running)
	s.mu.Unlock()
}

// Search evaluates query modifiers (#tag, @category, :running, :pinned) and free-text tokens,
// returning ranked matches sorted by match quality + Frecency.
func (s *SearchService) Search(rawQuery string, limit int) []domain.SearchHit {
	if limit <= 0 {
		limit = 30
	}

	var reqTags []string
	var reqCats []string
	var onlyRunning bool
	var onlyPinned bool
	var textTokens []string

	for _, tok := range strings.Fields(strings.TrimSpace(rawQuery)) {
		lt := strings.ToLower(tok)
		switch {
		case strings.HasPrefix(lt, "#") && len(lt) > 1:
			reqTags = append(reqTags, strings.TrimPrefix(lt, "#"))
		case strings.HasPrefix(lt, "@") && len(lt) > 1:
			reqCats = append(reqCats, strings.TrimPrefix(lt, "@"))
		case lt == ":running":
			onlyRunning = true
		case lt == ":pinned":
			onlyPinned = true
		default:
			textTokens = append(textTokens, lt)
		}
	}
	kw := strings.Join(textTokens, "")

	nowMs := time.Now().UnixMilli()

	s.mu.RLock()
	entries := s.entries
	running := s.running
	s.mu.RUnlock()

	hits := make([]domain.SearchHit, 0, len(entries))
	for _, entry := range entries {
		pids := running[entry.app.ID]
		isRun := len(pids) > 0

		if onlyRunning && !isRun {
			continue
		}
		if onlyPinned && !entry.app.IsPinned {
			continue
		}
		if !matchesAllFilters(entry.lowerTags, reqTags) {
			continue
		}
		if len(reqCats) > 0 && !matchesCatFilter(entry.lowerCat, reqCats) {
			continue
		}

		matchScore, reason := scoreTextMatch(kw, entry)
		if kw != "" && matchScore <= 0 {
			continue
		}

		frecency := computeFrecency(entry.app, isRun, nowMs)
		totalScore := matchScore*10.0 + frecency

		hydrated := entry.app
		hydrated.IsRunning = isRun
		if len(pids) > 0 {
			hydrated.RunningPIDs = append([]int(nil), pids...)
		} else {
			hydrated.RunningPIDs = []int{}
		}

		hits = append(hits, domain.SearchHit{
			App:         hydrated,
			Score:       totalScore,
			MatchReason: reason,
		})
	}

	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		if hits[i].App.LastLaunchedAt != hits[j].App.LastLaunchedAt {
			return hits[i].App.LastLaunchedAt > hits[j].App.LastLaunchedAt
		}
		return hits[i].App.Name < hits[j].App.Name
	})

	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits
}

func matchesAllFilters(have []string, required []string) bool {
	for _, req := range required {
		found := false
		for _, h := range have {
			if strings.Contains(h, req) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func matchesCatFilter(have string, required []string) bool {
	for _, req := range required {
		if !strings.Contains(have, req) {
			return false
		}
	}
	return true
}

func scoreTextMatch(kw string, e indexedApp) (float64, string) {
	if kw == "" {
		return 1.0, "recent"
	}

	// 1. Exact or prefix match on aliases
	for i, al := range e.lowerAliases {
		if al == kw {
			return 120, "alias"
		}
		if strings.HasPrefix(al, kw) {
			return 102, "alias"
		}
		if i < len(e.aliasInitials) && strings.HasPrefix(e.aliasInitials[i], kw) {
			return 92, "alias-pinyin"
		}
		if i < len(e.aliasPinyin) && strings.HasPrefix(e.aliasPinyin[i], kw) {
			return 88, "alias-pinyin"
		}
	}

	// 2. Exact or prefix match on name
	if e.lowerName == kw {
		return 115, "exact"
	}
	if strings.HasPrefix(e.lowerName, kw) {
		return 100, "prefix"
	}

	// 3. Pinyin initials & full pinyin match
	if e.pinyinInitials != "" {
		if e.pinyinInitials == kw {
			return 96, "pinyin-initials"
		}
		if strings.HasPrefix(e.pinyinInitials, kw) {
			return 90, "pinyin-initials"
		}
	}
	if e.pinyinFull != "" && strings.HasPrefix(e.pinyinFull, kw) {
		return 84, "pinyin"
	}

	// 4. Substring matches
	if strings.Contains(e.lowerName, kw) {
		return 74, "name"
	}
	for _, al := range e.lowerAliases {
		if strings.Contains(al, kw) {
			return 70, "alias"
		}
	}
	if strings.Contains(e.pinyinInitials, kw) {
		return 64, "pinyin-initials"
	}
	if strings.Contains(e.pinyinFull, kw) {
		return 58, "pinyin"
	}
	for _, t := range e.lowerTags {
		if strings.Contains(t, kw) {
			return 48, "tag"
		}
	}
	if strings.Contains(e.lowerCat, kw) {
		return 44, "category"
	}

	// 5. Fuzzy subsequence match on name and pinyin
	if fuzzyScore := fuzzySubsequenceScore(kw, e.lowerName); fuzzyScore > 0 {
		return 35 + fuzzyScore, "fuzzy"
	}
	if fuzzyScore := fuzzySubsequenceScore(kw, e.pinyinFull); fuzzyScore > 0 {
		return 28 + fuzzyScore, "fuzzy-pinyin"
	}
	if strings.Contains(e.lowerDesc, kw) {
		return 20, "description"
	}

	return 0, ""
}

func fuzzySubsequenceScore(query, target string) float64 {
	if query == "" || target == "" {
		return 0
	}
	qRunes := []rune(query)
	tRunes := []rune(target)
	if len(qRunes) > len(tRunes) {
		return 0
	}

	qi := 0
	consecutive := 0
	bonus := 0.0
	for ti, tr := range tRunes {
		if tr == qRunes[qi] {
			consecutive++
			bonus += float64(consecutive)
			if ti == 0 {
				bonus += 3
			}
			qi++
			if qi == len(qRunes) {
				density := float64(len(qRunes)) / float64(len(tRunes))
				return math.Min(15, bonus*0.8+density*6)
			}
		} else {
			consecutive = 0
		}
	}
	return 0
}

func computeFrecency(app domain.AppOverview, isRunning bool, nowMs int64) float64 {
	score := 0.0
	if app.IsPinned {
		score += 45.0
	}
	if isRunning {
		score += 18.0
	}
	if app.LaunchCount > 0 {
		score += math.Log1p(float64(app.LaunchCount)) * 12.0
	}
	if app.LastLaunchedAt > 0 && nowMs >= app.LastLaunchedAt {
		ageHours := float64(nowMs-app.LastLaunchedAt) / float64(time.Hour.Milliseconds())
		switch {
		case ageHours <= 24:
			score += 32.0
		case ageHours <= 24*7:
			score += 18.0
		case ageHours <= 24*30:
			score += 8.0
		default:
			score += 2.0
		}
	} else if app.CreatedAt > 0 && nowMs >= app.CreatedAt {
		// Newly added apps get a mild cold-start boost during the first 48 hours.
		createdHours := float64(nowMs-app.CreatedAt) / float64(time.Hour.Milliseconds())
		if createdHours <= 48 {
			score += 10.0
		}
	}
	return score
}

func computePinyin(text string) (full string, initials string) {
	args := pinyin.NewArgs()
	args.Style = pinyin.Normal

	var fullB strings.Builder
	var initB strings.Builder
	prevAlphaNum := false

	for _, r := range text {
		switch {
		case unicode.Is(unicode.Han, r):
			prevAlphaNum = false
			pys := pinyin.SinglePinyin(r, args)
			if len(pys) > 0 && pys[0] != "" {
				py := strings.ToLower(pys[0])
				fullB.WriteString(py)
				initB.WriteByte(py[0])
			}
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			lr := unicode.ToLower(r)
			fullB.WriteRune(lr)
			if !prevAlphaNum {
				initB.WriteRune(lr)
			}
			prevAlphaNum = true
		default:
			prevAlphaNum = false
		}
	}
	return fullB.String(), initB.String()
}

func copyRunningMap(src map[string][]int) map[string][]int {
	dst := make(map[string][]int, len(src))
	for k, v := range src {
		dst[k] = append([]int(nil), v...)
	}
	return dst
}
