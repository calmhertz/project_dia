import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../models/models.dart';
import '../state/session.dart';
import '../widgets/formatting.dart';
import '../widgets/states.dart';
import 'pass_detail_screen.dart';
import 'satellites_screen.dart' show describeError;

/// Everything the station has captured that this user may see
/// (client-spec section 20).
///
/// A recording is only ever reachable through a pass, so each row says which
/// pass produced it and the download goes through the same authorization the
/// pass has.
class RecordingsScreen extends StatefulWidget {
  const RecordingsScreen({super.key});

  @override
  State<RecordingsScreen> createState() => _RecordingsScreenState();
}

class _RecordingsScreenState extends State<RecordingsScreen> {
  late Future<({List<Recording> recordings, Map<String, Satellite> satellites})>
  _future;
  String _scope = 'mine';

  @override
  void initState() {
    super.initState();
    _load();
  }

  void _load() {
    final api = context.read<Session>().api;
    setState(() {
      _future = () async {
        final recordings = await api.recordings(scope: _scope);
        final catalogue = await api.satellites();
        return (
          recordings: recordings,
          satellites: {for (final entry in catalogue) entry.id: entry},
        );
      }();
    });
  }

  void _select(String scope) {
    _scope = scope;
    _load();
  }

  @override
  Widget build(BuildContext context) {
    final isAdmin = context.watch<Session>().user?.isAdmin ?? false;

    return Column(
      children: [
        Padding(
          padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
          child: Row(
            children: [
              ChoiceChip(
                label: const Text('Mine'),
                selected: _scope == 'mine',
                onSelected: (_) => _select('mine'),
              ),
              const SizedBox(width: 8),
              ChoiceChip(
                label: const Text('Public'),
                selected: _scope == 'public',
                onSelected: (_) => _select('public'),
              ),
              if (isAdmin) ...[
                const SizedBox(width: 8),
                ChoiceChip(
                  label: const Text('All'),
                  selected: _scope == 'all',
                  onSelected: (_) => _select('all'),
                ),
              ],
            ],
          ),
        ),
        Expanded(
          child:
              FutureBuilder<
                ({
                  List<Recording> recordings,
                  Map<String, Satellite> satellites,
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
                  if (data.recordings.isEmpty) {
                    return EmptyState(
                      icon: Icons.folder_off_outlined,
                      title: 'Nothing recorded yet',
                      message: switch (_scope) {
                        'public' =>
                          'Recordings from public passes will appear here.',
                        'all' => 'The station has captured nothing so far.',
                        _ =>
                          'Recordings from your passes will appear here once '
                              'they have run.',
                      },
                    );
                  }

                  return RefreshIndicator(
                    onRefresh: () async => _load(),
                    child: ListView.builder(
                      padding: const EdgeInsets.fromLTRB(16, 0, 16, 16),
                      itemCount: data.recordings.length,
                      itemBuilder: (context, index) {
                        final recording = data.recordings[index];
                        return _RecordingRow(
                          recording: recording,
                          satellite: recording.satelliteId == null
                              ? null
                              : data.satellites[recording.satelliteId],
                        );
                      },
                    ),
                  );
                },
              ),
        ),
      ],
    );
  }
}

class _RecordingRow extends StatelessWidget {
  const _RecordingRow({required this.recording, required this.satellite});

  final Recording recording;
  final Satellite? satellite;

  @override
  Widget build(BuildContext context) {
    final subtitle = <String>[
      formatBytes(recording.sizeBytes),
      if (recording.passAos != null) formatDateTime(recording.passAos!),
      recording.relativePath,
    ].join(' · ');

    return Card(
      margin: const EdgeInsets.only(bottom: 8),
      child: ListTile(
        leading: const Icon(Icons.insert_drive_file_outlined),
        title: Text(satellite?.name ?? 'Recording'),
        subtitle: Text(subtitle, maxLines: 2, overflow: TextOverflow.ellipsis),
        trailing: recording.isPublic
            ? const Icon(Icons.public, size: 18)
            : const Icon(Icons.chevron_right),
        // Downloading happens on the pass page, where the pass state and any
        // other output from the same pass are visible together.
        onTap: recording.passId == null
            ? null
            : () => Navigator.of(context).push(
                MaterialPageRoute(
                  builder: (_) => PassDetailScreen(
                    passId: recording.passId!,
                    satelliteName: satellite?.name,
                  ),
                ),
              ),
      ),
    );
  }
}
