package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"oneapp/internal/domain"
)

func TestSearchPinyinAliasModifiersAndFrecency(t *testing.T) {
	now := time.Now().UnixMilli()
	svc := NewSearchService()

	apps := []domain.AppOverview{
		{
			App: domain.App{
				ID:             "a1",
				Name:           "截图工具",
				Aliases:        []string{"snip"},
				LaunchCount:    12,
				LastLaunchedAt: now - int64(time.Hour),
				IsPinned:       true,
			},
			CategoryName: "效率工具",
			Tags:         []string{"截图", "GUI"},
		},
		{
			App: domain.App{
				ID:          "a2",
				Name:        "PortScanner",
				Aliases:     []string{"ps", "duankou"},
				LaunchCount: 3,
				CreatedAt:   now,
			},
			CategoryName: "自研工具",
			Tags:         []string{"网络", "CLI"},
		},
		{
			App: domain.App{
				ID:          "a3",
				Name:        "JSONFormatter",
				LaunchCount: 0,
				CreatedAt:   now - int64(100*time.Hour),
			},
			CategoryName: "效率工具",
			Tags:         []string{"文本"},
		},
	}

	running := map[string][]int{
		"a2": {4321},
	}
	svc.Rebuild(apps, running)

	// 1. Chinese pinyin initials
	hits := svc.Search("jt", 10)
	require.NotEmpty(t, hits)
	require.Equal(t, "a1", hits[0].App.ID)

	// 2. Chinese full pinyin
	hits = svc.Search("jietu", 10)
	require.NotEmpty(t, hits)
	require.Equal(t, "a1", hits[0].App.ID)

	// 3. Alias match
	hits = svc.Search("ps", 10)
	require.NotEmpty(t, hits)
	require.Equal(t, "a2", hits[0].App.ID)

	// 4. Tag + category modifiers
	hits = svc.Search("#网络 @自研", 10)
	require.Len(t, hits, 1)
	require.Equal(t, "a2", hits[0].App.ID)

	// 5. Running modifier
	hits = svc.Search(":running", 10)
	require.Len(t, hits, 1)
	require.Equal(t, "a2", hits[0].App.ID)
	require.True(t, hits[0].App.IsRunning)

	// 6. Fuzzy subsequence
	hits = svc.Search("ptsc", 10)
	require.NotEmpty(t, hits)
	require.Equal(t, "a2", hits[0].App.ID)

	// 7. Empty query orders by Frecency (a1 pinned & recently launched > a2 running > a3 unused)
	hits = svc.Search("", 10)
	require.Len(t, hits, 3)
	require.Equal(t, "a1", hits[0].App.ID)
	require.Equal(t, "a2", hits[1].App.ID)
	require.Equal(t, "a3", hits[2].App.ID)
}
