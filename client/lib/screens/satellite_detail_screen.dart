import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../models/models.dart';
import '../api/api_exception.dart';
import '../state/session.dart';
import '../widgets/formatting.dart';
import '../widgets/states.dart';
import 'root/edit_satellite_dialog.dart';
import 'satellites_screen.dart' show describeError;
import 'schedule_pass_screen.dart';

/// Satellite detail and its upcoming passes.
///
/// Raw provider and prediction internals stay hidden; a user sees times,
/// elevations and warnings (client-spec sections 9 and 10).
class SatelliteDetailScreen extends StatefulWidget {
  const SatelliteDetailScreen({super.key, required this.satelliteId});

  final String satelliteId;

  @override
  State<SatelliteDetailScreen> createState() => _SatelliteDetailScreenState();
}

class _SatelliteDetailScreenState extends State<SatelliteDetailScreen> {
  late Future<({Satellite satellite, TleSummary? tle})> _detail;
  late Future<PassPrediction> _passes;

  @override
  void initState() {
    super.initState();
    _load();
  }

  void _load() {
    final api = context.read<Session>().api;
    setState(() {
      // Marked observed so a failure is shown by the page rather than
      // escaping before its builder attaches.
      _detail = api.satellite(widget.satelliteId)..ignore();
      _passes = api.predictPasses(widget.satelliteId)..ignore();
    });
  }

  void _report(String message) {
    if (!mounted) return;
    ScaffoldMessenger.of(context)
        .showSnackBar(SnackBar(content: Text(message)));
  }

  /// Catalogue administration, Root only (spec.md section 17.1). The Server
  /// refuses these for anyone else whatever the client shows.
  Future<void> _edit(Satellite satellite) async {
    final changed = await showDialog<bool>(
      context: context,
      builder: (context) => EditSatelliteDialog(satellite: satellite),
    );
    if (changed == true) _load();
  }

  Future<void> _refreshMetadata() async {
    try {
      await context.read<Session>().api.refreshSatelliteMetadata(
        widget.satelliteId,
      );
      _report('Display information refreshed.');
      _load();
    } catch (error) {
      // Enrichment is optional: a provider outage changes nothing that
      // matters for tracking (spec.md section 11.2).
      _report(describeError(error));
    }
  }

  Future<void> _delete(Satellite satellite) async {
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: Text('Remove ${satellite.name}?'),
        content: const Text(
          'It disappears from the catalogue and cannot be scheduled. A '
          'satellite with passes on record cannot be removed at all.',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context, false),
            child: const Text('Keep'),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(context, true),
            child: const Text('Remove'),
          ),
        ],
      ),
    );
    if (confirmed != true || !mounted) return;

    try {
      await context.read<Session>().api.deleteSatellite(widget.satelliteId);
      if (!mounted) return;
      Navigator.of(context).pop();
      _report('${satellite.name} removed.');
    } on ApiException catch (error) {
      _report(
        error.isConflict
            ? 'This satellite has passes on record, so it cannot be removed. '
                  'Close it for scheduling instead.'
            : error.message,
      );
    } catch (error) {
      _report(describeError(error));
    }
  }

  @override
  Widget build(BuildContext context) {
    final isRoot = context.watch<Session>().user?.isRoot ?? false;

    return Scaffold(
      appBar: AppBar(
        title: const Text('Satellite'),
        actions: [
          if (isRoot)
            FutureBuilder<({Satellite satellite, TleSummary? tle})>(
              future: _detail,
              builder: (context, snapshot) {
                final satellite = snapshot.data?.satellite;
                if (satellite == null) return const SizedBox.shrink();
                return PopupMenuButton<String>(
                  tooltip: 'Catalogue',
                  onSelected: (value) => switch (value) {
                    'edit' => _edit(satellite),
                    'metadata' => _refreshMetadata(),
                    _ => _delete(satellite),
                  },
                  itemBuilder: (context) => const [
                    PopupMenuItem(value: 'edit', child: Text('Edit')),
                    PopupMenuItem(
                      value: 'metadata',
                      child: Text('Refresh display information'),
                    ),
                    PopupMenuDivider(),
                    PopupMenuItem(
                      value: 'delete',
                      child: Text('Remove from catalogue'),
                    ),
                  ],
                );
              },
            ),
        ],
      ),
      body: FutureBuilder<({Satellite satellite, TleSummary? tle})>(
        future: _detail,
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

          final satellite = snapshot.data!.satellite;
          final tle = snapshot.data!.tle;

          return ListView(
            padding: const EdgeInsets.all(16),
            children: [
              _Header(satellite: satellite, tle: tle),
              const SizedBox(height: 24),
              Text(
                'Upcoming passes',
                style: Theme.of(context).textTheme.titleMedium,
              ),
              const SizedBox(height: 8),
              _UpcomingPasses(
                future: _passes,
                satellite: satellite,
                onRetry: _load,
              ),
            ],
          );
        },
      ),
    );
  }
}

class _Header extends StatelessWidget {
  const _Header({required this.satellite, required this.tle});

  final Satellite satellite;
  final TleSummary? tle;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Card(
      child: Padding(
        padding: const EdgeInsets.all(16),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(satellite.name, style: theme.textTheme.headlineSmall),
            const SizedBox(height: 4),
            Text(
              'NORAD ${satellite.noradId}',
              style: theme.textTheme.bodyMedium?.copyWith(
                color: theme.colorScheme.onSurfaceVariant,
              ),
            ),
            if (satellite.description.isNotEmpty) ...[
              const SizedBox(height: 12),
              Text(satellite.description, style: theme.textTheme.bodyMedium),
            ],
            const SizedBox(height: 16),
            Wrap(
              spacing: 8,
              runSpacing: 8,
              children: [
                if (satellite.status != null)
                  _Fact('Status', satellite.status!),
                if (satellite.operator != null && satellite.operator != 'None')
                  _Fact('Operator', satellite.operator!),
                if (satellite.countries != null)
                  _Fact('Country', satellite.countries!),
                // Orbital data freshness matters operationally, so it is shown
                // as an age rather than a raw epoch.
                if (tle != null)
                  _Fact(
                    'Orbital data',
                    '${tle!.ageHours.toStringAsFixed(0)}h old',
                  ),
              ],
            ),
            if (tle == null) ...[
              const SizedBox(height: 12),
              Row(
                children: [
                  Icon(
                    Icons.warning_amber_outlined,
                    size: 18,
                    color: theme.colorScheme.error,
                  ),
                  const SizedBox(width: 8),
                  Expanded(
                    child: Text(
                      'No orbital data yet, so passes cannot be predicted.',
                      style: theme.textTheme.bodySmall,
                    ),
                  ),
                ],
              ),
            ],
          ],
        ),
      ),
    );
  }
}

class _Fact extends StatelessWidget {
  const _Fact(this.label, this.value);

  final String label;
  final String value;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
      decoration: BoxDecoration(
        color: theme.colorScheme.surfaceContainerHighest,
        borderRadius: BorderRadius.circular(8),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            label,
            style: theme.textTheme.labelSmall?.copyWith(
              color: theme.colorScheme.onSurfaceVariant,
            ),
          ),
          Text(value, style: theme.textTheme.bodyMedium),
        ],
      ),
    );
  }
}

class _UpcomingPasses extends StatelessWidget {
  const _UpcomingPasses({
    required this.future,
    required this.satellite,
    required this.onRetry,
  });

  final Future<PassPrediction> future;
  final Satellite satellite;
  final VoidCallback onRetry;

  @override
  Widget build(BuildContext context) {
    return FutureBuilder<PassPrediction>(
      future: future,
      builder: (context, snapshot) {
        if (snapshot.connectionState == ConnectionState.waiting) {
          return const Padding(
            padding: EdgeInsets.symmetric(vertical: 32),
            child: LoadingState(message: 'Predicting passes'),
          );
        }
        if (snapshot.hasError) {
          return Padding(
            padding: const EdgeInsets.symmetric(vertical: 24),
            child: ErrorState(
              title: 'Passes unavailable',
              message: describeError(snapshot.error),
              onRetry: onRetry,
            ),
          );
        }

        final prediction = snapshot.data!;
        if (prediction.passes.isEmpty) {
          return const Padding(
            padding: EdgeInsets.symmetric(vertical: 24),
            child: EmptyState(
              icon: Icons.schedule,
              title: 'No passes in the next 48 hours',
              message:
                  'This satellite does not rise above the station '
                  'threshold during that window.',
            ),
          );
        }

        return Column(
          children: [
            for (final pass in prediction.passes)
              _PassCard(
                pass: pass,
                satellite: satellite,
                stationBand: prediction.stationActiveBand,
              ),
          ],
        );
      },
    );
  }
}

class _PassCard extends StatelessWidget {
  const _PassCard({
    required this.pass,
    required this.satellite,
    required this.stationBand,
  });

  final PredictedPass pass;
  final Satellite satellite;
  final String stationBand;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Card(
      margin: const EdgeInsets.only(bottom: 8),
      child: InkWell(
        borderRadius: BorderRadius.circular(12),
        onTap: satellite.isSchedulable
            ? () => Navigator.of(context).push(
                MaterialPageRoute(
                  builder: (_) => SchedulePassScreen(
                    satellite: satellite,
                    pass: pass,
                    stationBand: stationBand,
                  ),
                ),
              )
            : null,
        child: Padding(
          padding: const EdgeInsets.all(16),
          child: Row(
            children: [
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(
                      formatDateTime(pass.aos),
                      style: theme.textTheme.titleSmall,
                    ),
                    const SizedBox(height: 4),
                    Text(
                      '${formatRelative(pass.aos)} · '
                      'max ${pass.maxElevationDegrees.toStringAsFixed(0)}° · '
                      '${formatDuration(pass.duration)}',
                      style: theme.textTheme.bodySmall?.copyWith(
                        color: theme.colorScheme.onSurfaceVariant,
                      ),
                    ),
                  ],
                ),
              ),
              if (satellite.isSchedulable)
                const Icon(Icons.chevron_right)
              else
                Text('Unavailable', style: theme.textTheme.labelMedium),
            ],
          ),
        ),
      ),
    );
  }
}
