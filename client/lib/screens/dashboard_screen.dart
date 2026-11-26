import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../models/models.dart';
import '../state/session.dart';
import '../widgets/formatting.dart';
import '../widgets/states.dart';
import 'pass_detail_screen.dart';
import 'satellites_screen.dart' show describeError;

class DashboardScreen extends StatefulWidget {
  const DashboardScreen({super.key});

  @override
  State<DashboardScreen> createState() => _DashboardScreenState();
}

class _DashboardScreenState extends State<DashboardScreen> {
  late Future<
    ({
      List<Pass> passes,
      Map<String, Satellite> satellites,
      List<WorkerStatus> workers,
      List<Pass> queue,
    })
  >
  _future;

  @override
  void initState() {
    super.initState();
    _load();
  }

  void _load() {
    final session = context.read<Session>();
    final api = session.api;
    final isAdmin = session.user?.isAdmin ?? false;
    setState(() {
      _future = () async {
        final passes = await api.passes();
        final catalogue = await api.satellites();
        List<WorkerStatus> workers = const [];
        List<Pass> queue = const [];
        if (isAdmin) {
          workers = (await api.workers()).workers;
          queue = await api.passes(scope: 'all', status: 'pending_approval');
        }
        return (
          passes: passes,
          satellites: {for (final entry in catalogue) entry.id: entry},
          workers: workers,
          queue: queue,
        );
      }();
    });
  }

  @override
  Widget build(BuildContext context) {
    final user = context.watch<Session>().user;

    return FutureBuilder<
      ({
        List<Pass> passes,
        Map<String, Satellite> satellites,
        List<WorkerStatus> workers,
        List<Pass> queue,
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

        final passes = snapshot.data!.passes;
        final satellites = snapshot.data!.satellites;
        final now = DateTime.now().toUtc();

        final upcoming =
            passes
                .where((pass) => pass.los.isAfter(now) && !pass.isFinished)
                .toList()
              ..sort((a, b) => a.aos.compareTo(b.aos));
        final awaitingApproval = upcoming
            .where((pass) => pass.isPending)
            .toList();
        final recent = passes.where((pass) => pass.isFinished).toList()
          ..sort((a, b) => b.aos.compareTo(a.aos));

        return RefreshIndicator(
          onRefresh: () async => _load(),
          child: ListView(
            padding: const EdgeInsets.all(16),
            children: [
              Text(
                'Welcome back, ${user?.username ?? ''}',
                style: Theme.of(context).textTheme.headlineSmall,
              ),
              const SizedBox(height: 24),

              if (user?.isAdmin ?? false) ...[
                _OperationalSummary(
                  workers: snapshot.data!.workers,
                  waiting: snapshot.data!.queue.length,
                ),
                const SizedBox(height: 24),
              ],

              _Section(
                title: 'Next passes',
                emptyMessage:
                    'Nothing scheduled. Pick a satellite to request one.',
                passes: upcoming.take(3).toList(),
                satellites: satellites,
                onChanged: _load,
              ),
              if (awaitingApproval.isNotEmpty)
                _Section(
                  title: 'Waiting for approval',
                  emptyMessage: '',
                  passes: awaitingApproval.take(3).toList(),
                  satellites: satellites,
                  onChanged: _load,
                ),
              _Section(
                title: 'Recent results',
                emptyMessage: 'Completed passes will appear here.',
                passes: recent.take(3).toList(),
                satellites: satellites,
                onChanged: _load,
              ),
            ],
          ),
        );
      },
    );
  }
}

class _Section extends StatelessWidget {
  const _Section({
    required this.title,
    required this.emptyMessage,
    required this.passes,
    required this.satellites,
    required this.onChanged,
  });

  final String title;
  final String emptyMessage;
  final List<Pass> passes;
  final Map<String, Satellite> satellites;
  final VoidCallback onChanged;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    if (passes.isEmpty && emptyMessage.isEmpty) return const SizedBox.shrink();

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text(title, style: theme.textTheme.titleMedium),
        const SizedBox(height: 8),
        if (passes.isEmpty)
          Card(
            child: Padding(
              padding: const EdgeInsets.all(16),
              child: Text(
                emptyMessage,
                style: theme.textTheme.bodyMedium?.copyWith(
                  color: theme.colorScheme.onSurfaceVariant,
                ),
              ),
            ),
          )
        else
          for (final pass in passes)
            Card(
              margin: const EdgeInsets.only(bottom: 8),
              child: ListTile(
                title: Text(satellites[pass.satelliteId]?.name ?? 'Satellite'),
                subtitle: Text(
                  '${formatDateTime(pass.aos)} · '
                  '${formatRelative(pass.aos)}',
                ),
                trailing: StatusChip(status: pass.status),
                onTap: () async {
                  await Navigator.of(context).push(
                    MaterialPageRoute(
                      builder: (_) => PassDetailScreen(
                        passId: pass.id,
                        satelliteName: satellites[pass.satelliteId]?.name,
                      ),
                    ),
                  );
                  onChanged();
                },
              ),
            ),
        const SizedBox(height: 24),
      ],
    );
  }
}

class _OperationalSummary extends StatelessWidget {
  const _OperationalSummary({required this.workers, required this.waiting});

  final List<WorkerStatus> workers;
  final int waiting;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final colours = theme.colorScheme;
    final offline = workers.where((worker) => !worker.isOnline).toList();
    final reachable = workers.isNotEmpty && offline.isEmpty;

    return Card(
      child: Padding(
        padding: const EdgeInsets.all(16),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                Icon(
                  reachable ? Icons.link : Icons.link_off,
                  color: reachable ? colours.primary : colours.error,
                ),
                const SizedBox(width: 12),
                Expanded(
                  child: Text(
                    workers.isEmpty
                        ? 'No ground station is registered'
                        : reachable
                        ? 'Ground station online'
                        : 'Ground station offline',
                    style: theme.textTheme.titleSmall,
                  ),
                ),
              ],
            ),
            const SizedBox(height: 8),
            Row(
              children: [
                Icon(
                  waiting == 0 ? Icons.done_all : Icons.rule,
                  color: colours.onSurfaceVariant,
                ),
                const SizedBox(width: 12),
                Expanded(
                  child: Text(
                    waiting == 0
                        ? 'Nothing waiting for approval'
                        : '$waiting request${waiting == 1 ? '' : 's'} '
                              'waiting for approval',
                    style: theme.textTheme.bodyMedium,
                  ),
                ),
              ],
            ),
          ],
        ),
      ),
    );
  }
}
