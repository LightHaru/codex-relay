package mux

import (
	"context"
	"encoding/json"
	"sort"
	"sync"

	"github.com/LightHaru/codex-relay/internal/protocol"
	"github.com/LightHaru/codex-relay/internal/state"
)

type threadCandidate struct {
	account state.Account
	thread  map[string]any
}

func (m *Multiplexer) aggregateThreadList(request protocol.Message) {
	entries := m.childEntries()
	type result struct {
		account state.Account
		threads []map[string]any
	}
	results := make(chan result, len(entries))
	var wait sync.WaitGroup
	for _, entry := range entries {
		wait.Add(1)
		go func(entry childEntry) {
			defer wait.Done()
			results <- result{account: entry.account, threads: m.listAllThreads(entry, request.Params)}
		}(entry)
	}
	wait.Wait()
	close(results)

	candidates := make(map[string][]threadCandidate)
	withoutID := make([]map[string]any, 0)
	for accountResult := range results {
		for _, thread := range accountResult.threads {
			if threadID, ok := thread["id"].(string); ok {
				candidates[threadID] = append(candidates[threadID], threadCandidate{
					account: accountResult.account,
					thread:  thread,
				})
			} else {
				withoutID = append(withoutID, thread)
			}
		}
	}
	threads := mergeThreadCandidates(m.store, candidates, withoutID)
	sortThreads(threads)
	encoded, err := json.Marshal(map[string]any{"data": threads, "nextCursor": nil})
	if err != nil {
		m.write(protocol.Failure(request.ID, -32603, "failed to merge thread list"))
		return
	}
	m.write(protocol.Success(request.ID, encoded))
}

func mergeThreadCandidates(store *state.Store, candidates map[string][]threadCandidate, withoutID []map[string]any) []map[string]any {
	threads := make([]map[string]any, 0, len(candidates)+len(withoutID))
	for threadID, threadCandidates := range candidates {
		selected := chooseThreadCandidate(store, threadID, threadCandidates)
		threads = append(threads, selected.thread)
		// The persisted assignment is authoritative when both the source and
		// the migrated copy are visible. This prevents the concurrent account
		// list responses from moving a failed-over chat back to Primary.
		if _, assigned := store.ThreadOwner(threadID); !assigned {
			_ = store.SetThreadOwner(threadID, selected.account.ID)
		}
	}
	threads = append(threads, withoutID...)
	return threads
}

func chooseThreadCandidate(store *state.Store, threadID string, candidates []threadCandidate) threadCandidate {
	if owner, ok := store.ThreadOwner(threadID); ok {
		for _, candidate := range candidates {
			if candidate.account.ID == owner {
				return candidate
			}
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].account.ID < candidates[j].account.ID
	})
	for _, candidate := range candidates {
		if candidate.account.Controller {
			return candidate
		}
	}
	return candidates[0]
}

func (m *Multiplexer) listAllThreads(entry childEntry, originalParams json.RawMessage) []map[string]any {
	var params map[string]any
	if json.Unmarshal(originalParams, &params) != nil {
		params = make(map[string]any)
	}
	params["limit"] = 500
	threads := make([]map[string]any, 0)
	seenCursors := make(map[string]struct{})
	var cursor string
	for {
		if cursor == "" {
			params["cursor"] = nil
		} else {
			params["cursor"] = cursor
		}
		encodedParams, _ := json.Marshal(params)
		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		response, err := entry.child.Request(ctx, "thread/list", encodedParams)
		cancel()
		if err != nil {
			return threads
		}
		var decoded struct {
			Data       []map[string]any `json:"data"`
			NextCursor *string          `json:"nextCursor"`
		}
		if json.Unmarshal(response.Result, &decoded) != nil {
			return threads
		}
		threads = append(threads, decoded.Data...)
		if decoded.NextCursor == nil || *decoded.NextCursor == "" {
			return threads
		}
		cursor = *decoded.NextCursor
		if _, repeated := seenCursors[cursor]; repeated {
			return threads
		}
		seenCursors[cursor] = struct{}{}
	}
}
