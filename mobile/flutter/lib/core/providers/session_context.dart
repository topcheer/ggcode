import 'dart:async';

/// All per-connection mutable state.
///
/// A fresh [SessionContext] is created on every new QR scan (clearState=true).
/// On reconnect (clearState=false) the existing context is preserved so the
/// client can send resume_from with the last known session + event ID.
///
/// This struct exists so that state from one workspace/session can never leak
/// into another — the caller simply replaces the whole object with a new one.
class SessionContext {
  // Identity
  String clientId = '';
  String sessionId = '';
  String liveUrl = '';

  // Event tracking
  String lastAppliedEventId = '';
  String lastDurableEventId = '';
  String resumeOverrideEventId = '';

  // Authority / projection
  int relayAuthorityEpoch = 0;
  bool hasAuthoritativeProjection = false;
  bool resumeCompleted = false;
  bool awaitingSnapshotProjection = false;
  Timer? snapshotProjectionTimeout;

  // Resume replay tracking
  int pendingReplayCount = 0;
  String pendingResumeMode = '';
  int? pendingActiveSessionBarrierOrdinal;
  String pendingActiveSessionBarrierEventId = '';

  // Gap recovery
  bool gapRecoveryScheduled = false;
  bool gapRecoveryDeferred = false;
  int gapRecoveryAttemptCount = 0;

  // Dedup window for event IDs
  final List<String> recentEventIds = <String>[];
  final Set<String> recentEventSet = <String>{};

  /// Whether we have enough state to send resume_from (vs resume_hello).
  bool get hasState =>
      sessionId.isNotEmpty && lastAppliedEventId.isNotEmpty;

  /// Cancel all timers — call before discarding a context.
  void dispose() {
    snapshotProjectionTimeout?.cancel();
    snapshotProjectionTimeout = null;
  }
}
