import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../../models/models.dart';
import '../../state/session.dart';
import '../../widgets/formatting.dart';
import '../../widgets/states.dart';
import '../satellites_screen.dart' show describeError;

class OperationsScreen extends StatefulWidget {
  const OperationsScreen({super.key});

  @override
  State<OperationsScreen> createState() => _OperationsScreenState();
}

class _OperationsScreenState extends State<OperationsScreen> {
  late Future<
    ({
      StationStatus station,
      List<WorkerStatus> workers,
      int offlineAfterSeconds,
      SchedulingSettings settings,
    })
  >
  _future;

  @override
  void initState() {
    super.initState();
    _load();
  }

  void _load() {
    final api = context.read<Session>().api;
    setState(() {
      _future = () async {
        final station = await api.stationStatus();
        final workers = await api.workers();
        final settings = await api.schedulingConfig();
        return (
          station: station,
          workers: workers.workers,
          offlineAfterSeconds: workers.offlineAfterSeconds,
          settings: settings,
        );
      }();
    });
  }

  @override
  Widget build(BuildContext context) {
    return FutureBuilder<
      ({
        StationStatus station,
        List<WorkerStatus> workers,
        int offlineAfterSeconds,
        SchedulingSettings settings,
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
        final theme = Theme.of(context);

        return RefreshIndicator(
          onRefresh: () async => _load(),
          child: ListView(
            padding: const EdgeInsets.all(16),
            children: [
              Text('Station', style: theme.textTheme.titleMedium),
              const SizedBox(height: 8),
              _StationCard(station: data.station),

              const SizedBox(height: 24),
              Text('Ground station link', style: theme.textTheme.titleMedium),
              const SizedBox(height: 8),
              if (data.workers.isEmpty)
                const Card(
                  child: Padding(
                    padding: EdgeInsets.all(16),
                    child: Text('No ground station is registered yet.'),
                  ),
                )
              else
                for (final worker in data.workers)
                  _WorkerCard(
                    worker: worker,
                    offlineAfterSeconds: data.offlineAfterSeconds,
                  ),

              const SizedBox(height: 24),
              Text('Scheduling rules', style: theme.textTheme.titleMedium),
              const SizedBox(height: 8),
              _SettingsCard(settings: data.settings),
              const SizedBox(height: 32),
            ],
          ),
        );
      },
    );
  }
}

class _StationCard extends StatelessWidget {
  const _StationCard({required this.station});

  final StationStatus station;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Card(
      child: Padding(
        padding: const EdgeInsets.all(16),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                Expanded(
                  child: Text(station.name, style: theme.textTheme.titleSmall),
                ),
                Chip(
                  avatar: const Icon(Icons.radio, size: 16),
                  label: Text('${station.activeRfBand.toUpperCase()} active'),
                  visualDensity: VisualDensity.compact,
                ),
              ],
            ),
            const SizedBox(height: 12),
            _Row(
              'Location',
              '${station.latitudeDegrees.toStringAsFixed(4)}, '
                  '${station.longitudeDegrees.toStringAsFixed(4)}',
            ),
            _Row('Altitude', '${station.altitudeM.toStringAsFixed(0)} m'),
            _Row('Timezone', station.timezone),
            if (station.antennaDescriptions.isNotEmpty)
              _Row('Antennas', station.antennaDescriptions.join(', ')),
          ],
        ),
      ),
    );
  }
}

class _WorkerCard extends StatelessWidget {
  const _WorkerCard({required this.worker, required this.offlineAfterSeconds});

  final WorkerStatus worker;
  final int offlineAfterSeconds;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final colours = theme.colorScheme;

    return Card(
      margin: const EdgeInsets.only(bottom: 8),
      child: Padding(
        padding: const EdgeInsets.all(16),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                Icon(
                  worker.isOnline ? Icons.link : Icons.link_off,
                  color: worker.isOnline ? colours.primary : colours.error,
                ),
                const SizedBox(width: 12),
                Expanded(
                  child: Text(
                    worker.isOnline ? 'Online' : 'Offline',
                    style: theme.textTheme.titleSmall,
                  ),
                ),
                Text(worker.name, style: theme.textTheme.bodySmall),
              ],
            ),
            const SizedBox(height: 12),
            if (worker.lastSeenAt != null)
              _Row('Last seen', formatRelative(worker.lastSeenAt!)),
            _Row('Desired state', worker.inSync ? 'Up to date' : 'Catching up'),
            if (worker.hasBacklog)
              _Row(
                'Waiting to send',
                '${worker.pendingUploads} recordings, '
                    '${worker.pendingReports} results',
              ),
            if (!worker.isOnline) ...[
              const SizedBox(height: 12),
              Text(
                'New passes cannot be scheduled while the station is '
                'unreachable. It is treated as offline after '
                '$offlineAfterSeconds seconds without contact.',
                style: theme.textTheme.bodySmall?.copyWith(
                  color: colours.onSurfaceVariant,
                ),
              ),
            ],
          ],
        ),
      ),
    );
  }
}

class _SettingsCard extends StatelessWidget {
  const _SettingsCard({required this.settings});

  final SchedulingSettings settings;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Card(
      child: Padding(
        padding: const EdgeInsets.all(16),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            _Row('Minimum lead time', formatDuration(settings.minimumLeadTime)),
            _Row(
              'Buffer before a pass',
              formatDuration(settings.prePassBuffer),
            ),
            _Row(
              'Buffer after a pass',
              formatDuration(settings.postPassBuffer),
            ),
            _Row(
              'Recording margins',
              '${formatDuration(settings.recordingPreRoll)} before, '
                  '${formatDuration(settings.recordingPostRoll)} after',
            ),
            _Row(
              'Minimum elevation',
              '${settings.minimumElevationDegrees.toStringAsFixed(0)} degrees',
            ),
            const SizedBox(height: 12),
            Text(
              'Only Root can change these.',
              style: theme.textTheme.bodySmall?.copyWith(
                color: theme.colorScheme.onSurfaceVariant,
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class _Row extends StatelessWidget {
  const _Row(this.label, this.value);

  final String label;
  final String value;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 4),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          SizedBox(
            width: 160,
            child: Text(
              label,
              style: theme.textTheme.bodyMedium?.copyWith(
                color: theme.colorScheme.onSurfaceVariant,
              ),
            ),
          ),
          Expanded(child: Text(value, style: theme.textTheme.bodyMedium)),
        ],
      ),
    );
  }
}
