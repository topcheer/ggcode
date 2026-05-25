package main

import (
	"database/sql"
	"log"
	"sync/atomic"
	"time"
)

const defaultStatsInterval = 30 * time.Second

type relayStoreStats struct {
	Rooms          int64
	RoomSessions   int64
	GlobalSessions int64
	RoomEvents     int64
	GlobalEvents   int64
}

func (s *relayStore) statsSnapshot() (relayStoreStats, error) {
	if s == nil || s.db == nil {
		return relayStoreStats{}, nil
	}
	rooms, err := s.queryCount(`SELECT COUNT(*) FROM relay_rooms`)
	if err != nil {
		return relayStoreStats{}, err
	}
	roomSessions, err := s.queryCount(`SELECT COUNT(*) FROM relay_sessions`)
	if err != nil {
		return relayStoreStats{}, err
	}
	globalSessions, err := s.queryCount(`SELECT COUNT(*) FROM relay_global_sessions`)
	if err != nil {
		return relayStoreStats{}, err
	}
	roomEvents, err := s.queryCount(`SELECT COUNT(*) FROM relay_events`)
	if err != nil {
		return relayStoreStats{}, err
	}
	globalEvents, err := s.queryCount(`SELECT COUNT(*) FROM relay_global_events`)
	if err != nil {
		return relayStoreStats{}, err
	}
	return relayStoreStats{
		Rooms:          rooms,
		RoomSessions:   roomSessions,
		GlobalSessions: globalSessions,
		RoomEvents:     roomEvents,
		GlobalEvents:   globalEvents,
	}, nil
}

func (s *relayStore) queryCount(query string) (int64, error) {
	var count int64
	if err := s.db.QueryRow(query).Scan(&count); err != nil && err != sql.ErrNoRows {
		return 0, err
	}
	return count, nil
}

type relayStats struct {
	serverConnects            uint64
	clientConnects            uint64
	serverDisconnects         uint64
	clientDisconnects         uint64
	persistedEvents           uint64
	persistErrors             uint64
	clientBroadcastDeliveries uint64
	serverForwards            uint64
	resumeRequests            uint64
	replayedEvents            uint64
	resumeIncremental         uint64
	resumeFullHistory         uint64
	resumeSnapshotRequired    uint64
	activeSessionChanges      uint64
	activeSessionHydrates     uint64
	hydratedEvents            uint64
	roomStoreHits             uint64
	roomStoreMisses           uint64
	roomDestroys              uint64
}

type relayStatsSnapshot struct {
	ActiveRooms               int
	RoomsWithServer           int
	ConnectedClients          int
	BufferedRoomEvents        int
	Store                     relayStoreStats
	ServerConnects            uint64
	ClientConnects            uint64
	ServerDisconnects         uint64
	ClientDisconnects         uint64
	PersistedEvents           uint64
	PersistErrors             uint64
	ClientBroadcastDeliveries uint64
	ServerForwards            uint64
	ResumeRequests            uint64
	ReplayedEvents            uint64
	ResumeIncremental         uint64
	ResumeFullHistory         uint64
	ResumeSnapshotRequired    uint64
	ActiveSessionChanges      uint64
	ActiveSessionHydrates     uint64
	HydratedEvents            uint64
	RoomStoreHits             uint64
	RoomStoreMisses           uint64
	RoomDestroys              uint64
}

func newRelayStats() *relayStats {
	return &relayStats{}
}

func (s *relayStats) recordConnect(role string) {
	switch role {
	case "server":
		atomic.AddUint64(&s.serverConnects, 1)
	case "client":
		atomic.AddUint64(&s.clientConnects, 1)
	}
}

func (s *relayStats) recordDisconnect(role string) {
	switch role {
	case "server":
		atomic.AddUint64(&s.serverDisconnects, 1)
	case "client":
		atomic.AddUint64(&s.clientDisconnects, 1)
	}
}

func (s *relayStats) recordPersistResult(ok bool) {
	if ok {
		atomic.AddUint64(&s.persistedEvents, 1)
		return
	}
	atomic.AddUint64(&s.persistErrors, 1)
}

func (s *relayStats) recordClientBroadcast(deliveries int) {
	if deliveries <= 0 {
		return
	}
	atomic.AddUint64(&s.clientBroadcastDeliveries, uint64(deliveries))
}

func (s *relayStats) recordForwardToServer() {
	atomic.AddUint64(&s.serverForwards, 1)
}

func (s *relayStats) recordResume(mode string, replayCount int) {
	atomic.AddUint64(&s.resumeRequests, 1)
	if replayCount > 0 {
		atomic.AddUint64(&s.replayedEvents, uint64(replayCount))
	}
	switch mode {
	case "incremental":
		atomic.AddUint64(&s.resumeIncremental, 1)
	case "full_history":
		atomic.AddUint64(&s.resumeFullHistory, 1)
	case "snapshot_required":
		atomic.AddUint64(&s.resumeSnapshotRequired, 1)
	}
}

func (s *relayStats) recordActiveSession(changed bool, hydratedEvents int) {
	if changed {
		atomic.AddUint64(&s.activeSessionChanges, 1)
	}
	if hydratedEvents > 0 {
		atomic.AddUint64(&s.activeSessionHydrates, 1)
		atomic.AddUint64(&s.hydratedEvents, uint64(hydratedEvents))
	}
}

func (s *relayStats) recordRoomStoreResult(hit bool) {
	if hit {
		atomic.AddUint64(&s.roomStoreHits, 1)
		return
	}
	atomic.AddUint64(&s.roomStoreMisses, 1)
}

func (s *relayStats) recordRoomDestroy() {
	atomic.AddUint64(&s.roomDestroys, 1)
}

func (s *relayStats) snapshot(h *hub) (relayStatsSnapshot, error) {
	var snap relayStatsSnapshot
	if h != nil {
		h.mu.RLock()
		snap.ActiveRooms = len(h.rooms)
		rooms := make([]*room, 0, len(h.rooms))
		for _, r := range h.rooms {
			rooms = append(rooms, r)
		}
		h.mu.RUnlock()
		for _, r := range rooms {
			r.mu.RLock()
			if r.server != nil {
				snap.RoomsWithServer++
			}
			snap.ConnectedClients += len(r.clients)
			snap.BufferedRoomEvents += len(r.history)
			r.mu.RUnlock()
		}
		storeSnap, err := h.store.statsSnapshot()
		if err != nil {
			return relayStatsSnapshot{}, err
		}
		snap.Store = storeSnap
	}
	snap.ServerConnects = atomic.LoadUint64(&s.serverConnects)
	snap.ClientConnects = atomic.LoadUint64(&s.clientConnects)
	snap.ServerDisconnects = atomic.LoadUint64(&s.serverDisconnects)
	snap.ClientDisconnects = atomic.LoadUint64(&s.clientDisconnects)
	snap.PersistedEvents = atomic.LoadUint64(&s.persistedEvents)
	snap.PersistErrors = atomic.LoadUint64(&s.persistErrors)
	snap.ClientBroadcastDeliveries = atomic.LoadUint64(&s.clientBroadcastDeliveries)
	snap.ServerForwards = atomic.LoadUint64(&s.serverForwards)
	snap.ResumeRequests = atomic.LoadUint64(&s.resumeRequests)
	snap.ReplayedEvents = atomic.LoadUint64(&s.replayedEvents)
	snap.ResumeIncremental = atomic.LoadUint64(&s.resumeIncremental)
	snap.ResumeFullHistory = atomic.LoadUint64(&s.resumeFullHistory)
	snap.ResumeSnapshotRequired = atomic.LoadUint64(&s.resumeSnapshotRequired)
	snap.ActiveSessionChanges = atomic.LoadUint64(&s.activeSessionChanges)
	snap.ActiveSessionHydrates = atomic.LoadUint64(&s.activeSessionHydrates)
	snap.HydratedEvents = atomic.LoadUint64(&s.hydratedEvents)
	snap.RoomStoreHits = atomic.LoadUint64(&s.roomStoreHits)
	snap.RoomStoreMisses = atomic.LoadUint64(&s.roomStoreMisses)
	snap.RoomDestroys = atomic.LoadUint64(&s.roomDestroys)
	return snap, nil
}

func (h *hub) logStats() {
	if h == nil || h.stats == nil {
		return
	}
	snap, err := h.stats.snapshot(h)
	if err != nil {
		log.Printf("[relay] stats snapshot failed: %v", err)
		return
	}
	log.Printf(
		"[relay] stats rooms=%d rooms_with_server=%d clients=%d buffered_events=%d db_rooms=%d db_room_sessions=%d db_global_sessions=%d db_room_events=%d db_global_events=%d server_connects=%d client_connects=%d server_disconnects=%d client_disconnects=%d persisted_events=%d persist_errors=%d client_broadcast_deliveries=%d server_forwards=%d resumes=%d replayed_events=%d resume_incremental=%d resume_full_history=%d resume_snapshot_required=%d active_session_changes=%d active_session_hydrates=%d hydrated_events=%d room_store_hits=%d room_store_misses=%d room_destroys=%d",
		snap.ActiveRooms,
		snap.RoomsWithServer,
		snap.ConnectedClients,
		snap.BufferedRoomEvents,
		snap.Store.Rooms,
		snap.Store.RoomSessions,
		snap.Store.GlobalSessions,
		snap.Store.RoomEvents,
		snap.Store.GlobalEvents,
		snap.ServerConnects,
		snap.ClientConnects,
		snap.ServerDisconnects,
		snap.ClientDisconnects,
		snap.PersistedEvents,
		snap.PersistErrors,
		snap.ClientBroadcastDeliveries,
		snap.ServerForwards,
		snap.ResumeRequests,
		snap.ReplayedEvents,
		snap.ResumeIncremental,
		snap.ResumeFullHistory,
		snap.ResumeSnapshotRequired,
		snap.ActiveSessionChanges,
		snap.ActiveSessionHydrates,
		snap.HydratedEvents,
		snap.RoomStoreHits,
		snap.RoomStoreMisses,
		snap.RoomDestroys,
	)
}

func shortToken(token string) string {
	if len(token) <= 8 {
		return token
	}
	return token[:8]
}
