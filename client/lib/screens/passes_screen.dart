import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../models/models.dart';
import '../state/session.dart';
import '../widgets/formatting.dart';
import '../widgets/states.dart';
import 'pass_detail_screen.dart';
import 'satellites_screen.dart' show describeError;

/// A list of passes: the user's own, or the public history.
class PassesScreen extends StatefulWidget {
  const PassesScreen({super.key, required this.scope});

  /// `mine` for the user's own passes, `public` for shared history.
  final String scope;

  @override
  State<PassesScreen> createState() => _PassesScreenState();
}

class _PassesScreenState extends State<PassesScreen> {
  late Future<List<Pass>> _future;
  Map<String, Satellite> _satellites = const {};

  @override
  void initState() {
    super.initState();
    _load();
  }

  void _load() {
    final api = context.read<Session>().api;
    setState(() {
      _future = () async {
        // Names come from the catalogue, so a pass row reads as a satellite
        // rather than an identifier.
        final results = await Future.wait([
          api.passes(scope: widget.scope),
          api.satellites(),
        ]);
        final passes = results[0] as List<Pass>;
        final catalogue = results[1] as List<Satellite>;
        _satellites = {for (final entry in catalogue) entry.id: entry};
        return passes;
      }();
    });
  }

  @override
  Widget build(BuildContext context) {
    final isPublic = widget.scope == 'public';

    return FutureBuilder<List<Pass>>(
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

        final passes = snapshot.data ?? const <Pass>[];
        if (passes.isEmpty) {
          return EmptyState(
            icon: isPublic ? Icons.public_off : Icons.event_note_outlined,
            title: isPublic ? 'No public passes yet' : 'You have no passes yet',
            message: isPublic
                ? 'Passes shared by other users will appear here once they run.'
                : 'Choose a satellite and request a pass to get started.',
          );
        }

        // Soonest first for upcoming work, most recent first for history.
        final ordered = [...passes]
          ..sort(
            (a, b) =>
                isPublic ? b.aos.compareTo(a.aos) : a.aos.compareTo(b.aos),
          );

        return RefreshIndicator(
          onRefresh: () async => _load(),
          child: ListView.builder(
            itemCount: ordered.length,
            itemBuilder: (context, index) {
              final pass = ordered[index];
              return _PassTile(
                pass: pass,
                satellite: _satellites[pass.satelliteId],
                onChanged: _load,
              );
            },
          ),
        );
      },
    );
  }
}

class _PassTile extends StatelessWidget {
  const _PassTile({
    required this.pass,
    this.satellite,
    required this.onChanged,
  });

  final Pass pass;
  final Satellite? satellite;
  final VoidCallback onChanged;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return ListTile(
      title: Text(satellite?.name ?? 'Satellite'),
      subtitle: Padding(
        padding: const EdgeInsets.only(top: 4),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(
              '${formatDateTime(pass.aos)} · ${formatRelative(pass.aos)}',
              style: theme.textTheme.bodySmall,
            ),
            const SizedBox(height: 6),
            Row(
              children: [
                StatusChip(status: pass.status),
                if (pass.isPublic) ...[
                  const SizedBox(width: 8),
                  Icon(
                    Icons.public,
                    size: 14,
                    color: theme.colorScheme.onSurfaceVariant,
                  ),
                ],
              ],
            ),
          ],
        ),
      ),
      isThreeLine: true,
      trailing: const Icon(Icons.chevron_right),
      onTap: () async {
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
    );
  }
}
