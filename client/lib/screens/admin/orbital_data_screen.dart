import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../../models/models.dart';
import '../../state/session.dart';
import '../../widgets/formatting.dart';
import '../../widgets/states.dart';
import '../satellites_screen.dart' show describeError;

/// Orbital data freshness across the catalogue, and the refresh an Admin may
/// run.
///
/// A refresh that fails keeps the previous data rather than discarding it
/// (spec.md section 11.1), so the result is reported in those terms.
class OrbitalDataScreen extends StatefulWidget {
  const OrbitalDataScreen({super.key});

  @override
  State<OrbitalDataScreen> createState() => _OrbitalDataScreenState();
}

class _OrbitalDataScreenState extends State<OrbitalDataScreen> {
  late Future<TleStatus> _future;
  bool _refreshing = false;

  @override
  void initState() {
    super.initState();
    _load();
  }

  void _load() {
    final api = context.read<Session>().api;
    setState(() {
      _future = api.tleStatus()..ignore();
    });
  }

  Future<void> _refresh() async {
    setState(() => _refreshing = true);
    try {
      final result = await context.read<Session>().api.refreshTles();
      if (!mounted) return;
      final kept = result.keptLastKnownGood;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text(
            '${result.updated} updated, ${result.failed} without new data'
            '${kept > 0 ? ', $kept kept their last known good' : ''}.',
          ),
        ),
      );
      _load();
    } catch (error) {
      if (!mounted) return;
      ScaffoldMessenger.of(context)
          .showSnackBar(SnackBar(content: Text(describeError(error))));
    } finally {
      if (mounted) setState(() => _refreshing = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    return FutureBuilder<TleStatus>(
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

        final status = snapshot.data!;
        if (status.satellites.isEmpty) {
          return const EmptyState(
            icon: Icons.satellite_alt_outlined,
            title: 'No satellites yet',
            message: 'Root adds satellites to the catalogue.',
          );
        }

        final theme = Theme.of(context);
        return Column(
          children: [
            Padding(
              padding: const EdgeInsets.fromLTRB(16, 16, 16, 8),
              child: Row(
                children: [
                  Expanded(
                    child: Text(
                      _summarize(status),
                      style: theme.textTheme.bodyMedium,
                    ),
                  ),
                  const SizedBox(width: 12),
                  FilledButton.tonalIcon(
                    onPressed: _refreshing ? null : _refresh,
                    icon: _refreshing
                        ? const SizedBox(
                            height: 16,
                            width: 16,
                            child: CircularProgressIndicator(strokeWidth: 2),
                          )
                        : const Icon(Icons.refresh),
                    label: const Text('Refresh all'),
                  ),
                ],
              ),
            ),
            Expanded(
              child: RefreshIndicator(
                onRefresh: () async => _load(),
                child: ListView.builder(
                  padding: const EdgeInsets.fromLTRB(16, 0, 16, 16),
                  itemCount: status.satellites.length,
                  itemBuilder: (context, index) => _EntryTile(
                    entry: status.satellites[index],
                    staleAfterHours: status.staleAfterHours,
                  ),
                ),
              ),
            ),
          ],
        );
      },
    );
  }

  static String _summarize(TleStatus status) {
    final total = status.satellites.length;
    if (status.missingCount == 0 && status.staleCount == 0) {
      return 'All $total satellites have current orbital data.';
    }
    final parts = <String>[];
    if (status.staleCount > 0) {
      parts.add(
        '${status.staleCount} older than ${status.staleAfterHours} hours',
      );
    }
    if (status.missingCount > 0) {
      parts.add('${status.missingCount} with no data at all');
    }
    return 'Of $total satellites: ${parts.join(', ')}.';
  }
}

class _EntryTile extends StatelessWidget {
  const _EntryTile({required this.entry, required this.staleAfterHours});

  final TleStatusEntry entry;
  final int staleAfterHours;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final colours = theme.colorScheme;

    // Missing and stale are different problems and are shown as such.
    final (IconData icon, Color colour, String detail) = switch (entry) {
      TleStatusEntry(hasOrbitalData: false) => (
        Icons.help_outline,
        colours.error,
        'No orbital data yet',
      ),
      TleStatusEntry(stale: true) => (
        Icons.warning_amber_outlined,
        colours.error,
        'Epoch ${formatRelative(entry.epoch!)}, older than '
            '$staleAfterHours hours',
      ),
      _ => (
        Icons.check_circle_outline,
        colours.primary,
        'Epoch ${formatRelative(entry.epoch!)}'
            '${entry.source == null ? '' : ' from ${entry.source}'}',
      ),
    };

    return Card(
      margin: const EdgeInsets.only(bottom: 8),
      child: ListTile(
        leading: Icon(icon, color: colour),
        title: Text(entry.name),
        subtitle: Text('NORAD ${entry.noradId} · $detail'),
      ),
    );
  }
}
