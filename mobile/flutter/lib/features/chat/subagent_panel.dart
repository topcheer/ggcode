import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../core/providers/session_provider.dart';

/// Sub-agent live activity cards.
/// Shows floating cards at the bottom of the chat screen, like iOS Live Activities.
/// Each card shows agent name, status, streaming text preview.
/// Tapping expands to show full output.
class SubagentPanel extends ConsumerStatefulWidget {
  const SubagentPanel({super.key});

  @override
  ConsumerState<SubagentPanel> createState() => _SubagentPanelState();
}

class _SubagentPanelState extends ConsumerState<SubagentPanel> {
  final Set<String> _expanded = {};

  @override
  Widget build(BuildContext context) {
    final agents = ref.watch(subagentProvider);
    final active = agents.values.where((a) => !a.completed || _expanded.contains(a.agentId)).toList();

    if (active.isEmpty) return const SizedBox.shrink();

    return Positioned(
      left: 8,
      right: 8,
      bottom: 8,
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: active.map((agent) => _buildCard(agent)).toList(),
      ),
    );
  }

  Widget _buildCard(SubagentInfo agent) {
    final isExpanded = _expanded.contains(agent.agentId);
    final color = _parseColor(agent.color);
    final isRunning = agent.status == 'running' || agent.status == 'waiting_approval';

    return GestureDetector(
      onTap: () {
        setState(() {
          if (isExpanded) {
            _expanded.remove(agent.agentId);
          } else {
            _expanded.add(agent.agentId);
          }
        });
      },
      child: Container(
        margin: const EdgeInsets.only(bottom: 8),
        decoration: BoxDecoration(
          color: const Color(0xFF1E1E2E),
          borderRadius: BorderRadius.circular(16),
          border: Border.all(color: color.withOpacity(0.3)),
          boxShadow: [
            BoxShadow(
              color: Colors.black.withOpacity(0.3),
              blurRadius: 8,
              offset: const Offset(0, 2),
            ),
          ],
        ),
        child: ClipRRect(
          borderRadius: BorderRadius.circular(16),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              // Header
              Container(
                padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
                child: Row(
                  children: [
                    // Animated dot
                    if (isRunning)
                      SizedBox(
                        width: 8,
                        height: 8,
                        child: CircularProgressIndicator(
                          strokeWidth: 2,
                          color: color,
                        ),
                      )
                    else if (agent.completed)
                      Icon(
                        agent.success ? Icons.check_circle : Icons.error,
                        size: 16,
                        color: agent.success ? Colors.green : Colors.red,
                      ),
                    const SizedBox(width: 8),
                    // Name
                    Text(
                      agent.name,
                      style: TextStyle(
                        color: color,
                        fontWeight: FontWeight.w600,
                        fontSize: 13,
                      ),
                    ),
                    const SizedBox(width: 8),
                    // Task (truncated)
                    Expanded(
                      child: Text(
                        agent.task,
                        style: TextStyle(
                          color: Colors.white.withOpacity(0.6),
                          fontSize: 12,
                        ),
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                      ),
                    ),
                    // Expand icon
                    Icon(
                      isExpanded ? Icons.expand_less : Icons.expand_more,
                      color: Colors.white54,
                      size: 20,
                    ),
                  ],
                ),
              ),
              // Expanded content
              if (isExpanded)
                Container(
                  width: double.infinity,
                  padding: const EdgeInsets.fromLTRB(12, 0, 12, 12),
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      const Divider(color: Colors.white12, height: 1),
                      const SizedBox(height: 8),
                      // Streaming text or summary
                      Consumer(builder: (context, ref, _) {
                        final chatMessages = ref.watch(chatProvider);
                        final agentMessages = chatMessages
                            .where((m) => m.sourceId == agent.agentId)
                            .toList();

                        if (agentMessages.isEmpty && agent.summary != null) {
                          return Text(
                            agent.summary!,
                            style: const TextStyle(color: Colors.white70, fontSize: 13),
                          );
                        }

                        return Column(
                          crossAxisAlignment: CrossAxisAlignment.start,
                          children: agentMessages.map((msg) {
                            return Padding(
                              padding: const EdgeInsets.only(bottom: 4),
                              child: Row(
                                crossAxisAlignment: CrossAxisAlignment.start,
                                children: [
                                  if (msg.streaming)
                                    Container(
                                      margin: const EdgeInsets.only(top: 4, right: 6),
                                      width: 6,
                                      height: 6,
                                      decoration: BoxDecoration(
                                        color: color,
                                        shape: BoxShape.circle,
                                      ),
                                    ),
                                  Expanded(
                                    child: Text(
                                      msg.text,
                                      style: TextStyle(
                                        color: Colors.white.withOpacity(0.85),
                                        fontSize: 13,
                                        fontFamily: 'monospace',
                                      ),
                                    ),
                                  ),
                                ],
                              ),
                            );
                          }).toList(),
                        );
                      }),
                    ],
                  ),
                ),
            ],
          ),
        ),
      ),
    );
  }

  Color _parseColor(String hex) {
    try {
      return Color(int.parse(hex.replaceFirst('#', '0xFF')));
    } catch (_) {
      return Colors.green;
    }
  }
}
