import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../../models/models.dart';
import '../../state/session.dart';
import '../../widgets/formatting.dart';
import '../../widgets/states.dart';
import '../pass_detail_screen.dart';
import '../satellites_screen.dart' show describeError;

class ApprovalsScreen extends StatefulWidget {
  const ApprovalsScreen({super.key});

  @override
  State<ApprovalsScreen> createState() => _ApprovalsScreenState();
}

class _ApprovalsScreenState extends State<ApprovalsScreen> {
  late Future<
    ({
      List<Pass> pending,
      Map<String, Satellite> satellites,
      Map<String, String> usernames,
    })
  >
  _future;
  String? _deciding;

  @override
  void initState() {
    super.initState();
    _load();
  }

  void _load() {
    final api = context.read<Session>().api;
    setState(() {
      _deciding = null;
      _future = () async {
        final passes = await api.passes(
          scope: 'all',
          status: 'pending_approval',
        );
        final catalogue = await api.satellites();
        Map<String, String> usernames = {};
        try {
          usernames = {
            for (final user in await api.users()) user.id: user.username,
          };
        } on Object {
          usernames = {};
        }
        return (
          pending: passes..sort((a, b) => a.aos.compareTo(b.aos)),
          satellites: {for (final entry in catalogue) entry.id: entry},
          usernames: usernames,
        );
      }();
    });
  }

  Future<void> _decide(Pass pass, bool approve) async {
    setState(() => _deciding = pass.id);
    final api = context.read<Session>().api;
    try {
      final updated = approve
          ? await api.approvePass(pass.id)
          : await api.rejectPass(pass.id);
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Pass ${_describeStatus(updated.status)}.')),
      );
      _load();
    } catch (error) {
      if (!mounted) return;
      setState(() => _deciding = null);
      ScaffoldMessenger.of(context)
          .showSnackBar(SnackBar(content: Text(describeError(error))));
    }
  }

  static String _describeStatus(String status) => switch (status) {
    'approved' => 'approved',
    'rejected' => 'rejected',
    _ => status,
  };

  @override
  Widget build(BuildContext context) {
    return FutureBuilder<
      ({
        List<Pass> pending,
        Map<String, Satellite> satellites,
        Map<String, String> usernames,
      })
    >(
      future: _future,
      builder: (context, snapshot) {
        if (snapshot.connectionState == ConnectionState.waiting) {
          return const LoadingState();
        }
        if (snapshot.hasError) {
          return ErrorState(
            message: describeError(snapshot.error),
            onRetry: _load,
          );
        }

        final data = snapshot.data!;
        if (data.pending.isEmpty) {
          return const EmptyState(
            icon: Icons.done_all,
            title: 'Nothing waiting',
            message: 'Requests needing a decision will appear here.',
          );
        }

        return RefreshIndicator(
          onRefresh: () async => _load(),
          child: ListView.builder(
            padding: const EdgeInsets.all(16),
            itemCount: data.pending.length,
            itemBuilder: (context, index) {
              final pass = data.pending[index];
              return _RequestCard(
                pass: pass,
                satellite: data.satellites[pass.satelliteId],
                requester: pass.requestedBy == null
                    ? null
                    : data.usernames[pass.requestedBy],
                busy: _deciding == pass.id,
                onApprove: () => _decide(pass, true),
                onReject: () => _decide(pass, false),
                onChanged: _load,
              );
            },
          ),
        );
      },
    );
  }
}

class _RequestCard extends StatelessWidget {
  const _RequestCard({
    required this.pass,
    required this.satellite,
    required this.requester,
    required this.busy,
    required this.onApprove,
    required this.onReject,
    required this.onChanged,
  });

  final Pass pass;
  final Satellite? satellite;

  final String? requester;
  final bool busy;
  final VoidCallback onApprove;
  final VoidCallback onReject;
  final VoidCallback onChanged;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);

    return Card(
      margin: const EdgeInsets.only(bottom: 12),
      child: Padding(
        padding: const EdgeInsets.all(16),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                Expanded(
                  child: Text(
                    satellite?.name ?? 'Satellite',
                    style: theme.textTheme.titleMedium,
                  ),
                ),
                if (pass.isPublic)
                  const Chip(
                    avatar: Icon(Icons.public, size: 16),
                    label: Text('Public'),
                    visualDensity: VisualDensity.compact,
                  ),
              ],
            ),
            const SizedBox(height: 4),
            Text(
              '${formatDateTime(pass.aos)} to ${formatTime(pass.los)} · '
              '${formatRelative(pass.aos)}',
              style: theme.textTheme.bodySmall?.copyWith(
                color: theme.colorScheme.onSurfaceVariant,
              ),
            ),
            const SizedBox(height: 12),

            Wrap(
              spacing: 8,
              runSpacing: 8,
              children: [
                _Fact(icon: Icons.radio, text: pass.band.toUpperCase()),
                _Fact(
                  icon: Icons.save_alt,
                  text: describeRecordingMode(pass.recordingMode),
                ),
                _Fact(
                  icon: Icons.trending_up,
                  text: 'max ${pass.maxElevationDegrees.toStringAsFixed(0)}',
                ),
                _Fact(
                  icon: Icons.person_outline,
                  text: requester ?? pass.requestedBy ?? 'Requester withheld',
                ),
              ],
            ),
            const SizedBox(height: 16),

            Row(
              children: [
                TextButton(
                  onPressed: busy
                      ? null
                      : () async {
                          await Navigator.of(context).push(
                            MaterialPageRoute(
                              builder: (_) => PassDetailScreen(
                                passId: pass.id,
                                satelliteName: satellite?.name,
                              ),
                            ),
                          );
                          onChanged();
                        },
                  child: const Text('Details'),
                ),
                const Spacer(),
                OutlinedButton.icon(
                  onPressed: busy ? null : onReject,
                  icon: const Icon(Icons.close),
                  label: const Text('Reject'),
                  style: OutlinedButton.styleFrom(
                    foregroundColor: theme.colorScheme.error,
                  ),
                ),
                const SizedBox(width: 8),
                FilledButton.icon(
                  onPressed: busy ? null : onApprove,
                  icon: busy
                      ? const SizedBox(
                          height: 16,
                          width: 16,
                          child: CircularProgressIndicator(strokeWidth: 2),
                        )
                      : const Icon(Icons.check),
                  label: const Text('Approve'),
                ),
              ],
            ),
          ],
        ),
      ),
    );
  }
}

class _Fact extends StatelessWidget {
  const _Fact({required this.icon, required this.text});

  final IconData icon;
  final String text;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        Icon(icon, size: 16, color: theme.colorScheme.onSurfaceVariant),
        const SizedBox(width: 4),
        Text(text, style: theme.textTheme.bodySmall),
      ],
    );
  }
}
