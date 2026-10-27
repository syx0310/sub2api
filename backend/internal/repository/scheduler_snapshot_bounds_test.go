package repository

import (
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestSchedulerSnapshotMemoBounds(t *testing.T) {
	for _, mode := range []string{"entries", "accounts", "bytes"} {
		t.Run(mode, func(t *testing.T) {
			c := newSchedulerCacheWithChunkSizes(nil, 0, 0).(*schedulerCache)
			now := time.Now()
			for i := 0; i < schedulerSnapshotCacheMaxEntries+10; i++ {
				entry := &schedulerSnapshotEntry{loadedAt: now.Add(time.Duration(i) * time.Millisecond)}
				if mode == "accounts" {
					entry.accounts = make([]*service.Account, schedulerSnapshotCacheMaxAccounts/2)
				}
				if mode == "bytes" {
					entry.sourceBytes = schedulerSnapshotCacheMaxSourceBytes / 2
				}
				c.storeSnapshotEntry(fmt.Sprint(i), entry)
			}
			require.LessOrEqual(t, len(c.snapshotEntries), schedulerSnapshotCacheMaxEntries)
			require.NotNil(t, c.lookupSnapshotEntry(fmt.Sprint(schedulerSnapshotCacheMaxEntries+9)))
			if mode != "entries" {
				require.Len(t, c.snapshotEntries, 2)
			}
		})
	}
	c := newSchedulerCacheWithChunkSizes(nil, 0, 0).(*schedulerCache)
	c.storeSnapshotEntry("large", &schedulerSnapshotEntry{sourceBytes: schedulerSnapshotCacheMaxSourceBytes + 1})
	require.Nil(t, c.lookupSnapshotEntry("large"))
	c.storeSnapshotEntry("large", &schedulerSnapshotEntry{accounts: make([]*service.Account, schedulerSnapshotCacheMaxAccounts+1)})
	require.Nil(t, c.lookupSnapshotEntry("large"))
}
