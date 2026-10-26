import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../models/models.dart';
import '../services/download.dart';
import '../state/session.dart';
import '../widgets/formatting.dart';
import '../widgets/states.dart';
import 'satellites_screen.dart' show describeError;

class PassDetailScreen extends StatefulWidget {
  const PassDetailScreen({super.key, required this.passId, this.satelliteName});

  final String passId;
  final String? satelliteName;

  @override
  State<PassDetailScreen> createState() => _PassDetailScreenState();
}

class _PassDetailScreenState extends State<PassDetailScreen> {
  late Future<Pass> _pass;
  late Future<List<Recording>> _recordings;
  bool _cancelling = false;
  bool _deciding = false;

  @override
  void initState() {
    super.initState();
    _load();
  }

  void _load() {
    final api = context.read<Session>().api;
    setState(() {
      _pass = api.pass(widget.passId)..ignore();
      _recordings = api.recordingsForPass(widget.passId)..ignore();
    });
  }

  Future<void> _decide(bool approve) async {
    setState(() => _deciding = true);
    try {
      final api = context.read<Session>().api;
      final updated = approve
          ? await api.approvePass(widget.passId)
          : await api.rejectPass(widget.passId);
      if (!mounted) return;
      ScaffoldMessenger.of(context)
          .showSnackBar(SnackBar(content: Text('Pass ${updated.status}.')));
      _load();
    } catch (error) {
      if (mounted) {
        ScaffoldMessenger.of(context)
            .showSnackBar(SnackBar(content: Text(describeError(error))));
      }
    } finally {
      if (mounted) setState(() => _deciding = false);
    }
  }

  Future<void> _cancel() async {
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('Cancel this pass?'),
        content: const Text(
          'The station will not execute it. This cannot be undone.',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context, false),
            child: const Text('Keep it'),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(context, true),
            child: const Text('Cancel pass'),
          ),
        ],
      ),
    );
    if (confirmed != true || !mounted) return;

    setState(() => _cancelling = true);
    try {
      await context.read<Session>().api.cancelPass(widget.passId);
      if (mounted) _load();
    } catch (error) {
      if (mounted) {
        ScaffoldMessenger.of(context)
            .showSnackBar(SnackBar(content: Text(describeError(error))));
      }
    } finally {
      if (mounted) setState(() => _cancelling = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: Text(widget.satelliteName ?? 'Pass')),
      body: FutureBuilder<Pass>(
        future: _pass,
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

          final pass = snapshot.data!;
          final theme = Theme.of(context);
          final canCancel =
              pass.status == 'pending_approval' || pass.status == 'approved';
          final canDecide =
              pass.isPending &&
              (context.watch<Session>().user?.isAdmin ?? false);

          return ListView(
            padding: const EdgeInsets.all(16),
            children: [
              Row(
                children: [
                  StatusChip(status: pass.status),
                  const Spacer(),
                  if (pass.isPublic)
                    const Chip(
                      avatar: Icon(Icons.public, size: 16),
                      label: Text('Public'),
                    ),
                ],
              ),
              if (pass.cancellationReason == 'cancelled_by_root_override') ...[
                const SizedBox(height: 12),
                _Notice(
                  icon: Icons.swap_horiz,
                  message:
                      'This pass was cancelled because an administrator '
                      'scheduled a higher-priority operation in its place.',
                ),
              ],
              const SizedBox(height: 20),

              Card(
                child: Padding(
                  padding: const EdgeInsets.all(16),
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text('Pass window', style: theme.textTheme.titleSmall),
                      const SizedBox(height: 12),
                      _Row('Date', formatDate(pass.aos)),
                      _Row('Starts', formatTime(pass.aos)),
                      _Row('Ends', formatTime(pass.los)),
                      _Row(
                        'Highest point',
                        '${pass.maxElevationDegrees.toStringAsFixed(0)}°',
                      ),
                      _Row('Band', pass.band.toUpperCase()),
                      _Row(
                        'Produces',
                        describeRecordingMode(pass.recordingMode),
                      ),
                      if (pass.requestedBy != null)
                        _Row('Requested by', pass.requestedBy!),
                    ],
                  ),
                ),
              ),

              const SizedBox(height: 24),
              Text('Recordings', style: theme.textTheme.titleMedium),
              const SizedBox(height: 8),
              _Recordings(future: _recordings, pass: pass, onRetry: _load),

              if (canDecide) ...[
                const SizedBox(height: 24),
                Text('Decision', style: theme.textTheme.titleMedium),
                const SizedBox(height: 8),
                Row(
                  children: [
                    Expanded(
                      child: OutlinedButton.icon(
                        onPressed: _deciding ? null : () => _decide(false),
                        icon: const Icon(Icons.close),
                        label: const Text('Reject'),
                        style: OutlinedButton.styleFrom(
                          foregroundColor: theme.colorScheme.error,
                        ),
                      ),
                    ),
                    const SizedBox(width: 12),
                    Expanded(
                      child: FilledButton.icon(
                        onPressed: _deciding ? null : () => _decide(true),
                        icon: const Icon(Icons.check),
                        label: const Text('Approve'),
                      ),
                    ),
                  ],
                ),
              ],

              if (canCancel) ...[
                const SizedBox(height: 32),
                OutlinedButton.icon(
                  onPressed: _cancelling ? null : _cancel,
                  icon: const Icon(Icons.cancel_outlined),
                  label: const Text('Cancel this pass'),
                  style: OutlinedButton.styleFrom(
                    foregroundColor: theme.colorScheme.error,
                  ),
                ),
              ],
            ],
          );
        },
      ),
    );
  }
}

class _Recordings extends StatelessWidget {
  const _Recordings({
    required this.future,
    required this.pass,
    required this.onRetry,
  });

  final Future<List<Recording>> future;
  final Pass pass;
  final VoidCallback onRetry;

  @override
  Widget build(BuildContext context) {
    return FutureBuilder<List<Recording>>(
      future: future,
      builder: (context, snapshot) {
        if (snapshot.connectionState == ConnectionState.waiting) {
          return const Padding(
            padding: EdgeInsets.symmetric(vertical: 24),
            child: LoadingState(),
          );
        }
        if (snapshot.hasError) {
          return Padding(
            padding: const EdgeInsets.symmetric(vertical: 16),
            child: ErrorState(
              message: describeError(snapshot.error),
              onRetry: onRetry,
            ),
          );
        }

        final recordings = snapshot.data ?? const <Recording>[];
        if (recordings.isEmpty) {
          return Card(
            child: Padding(
              padding: const EdgeInsets.all(16),
              child: Row(
                children: [
                  Icon(
                    Icons.folder_off_outlined,
                    color: Theme.of(context).colorScheme.onSurfaceVariant,
                  ),
                  const SizedBox(width: 12),
                  Expanded(
                    child: Text(
                      pass.isFinished
                          ? 'This pass produced no recordings.'
                          : 'Recordings appear here once the pass has run.',
                      style: Theme.of(context).textTheme.bodyMedium,
                    ),
                  ),
                ],
              ),
            ),
          );
        }

        return Column(
          children: [
            for (final recording in recordings)
              _RecordingTile(recording: recording),
          ],
        );
      },
    );
  }
}

class _RecordingTile extends StatefulWidget {
  const _RecordingTile({required this.recording});

  final Recording recording;

  @override
  State<_RecordingTile> createState() => _RecordingTileState();
}

class _RecordingTileState extends State<_RecordingTile> {
  bool _busy = false;

  Future<void> _download() async {
    setState(() => _busy = true);
    try {
      final bytes = await context.read<Session>().api.downloadRecording(
        widget.recording,
      );
      final fileName = recordingFileName(widget.recording.relativePath);
      saveRecordingDownload(fileName, bytes);
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text(
            'Saved $fileName (${formatBytes(bytes.length)})',
          ),
        ),
      );
    } catch (error) {
      if (mounted) {
        ScaffoldMessenger.of(context)
            .showSnackBar(SnackBar(content: Text(describeError(error))));
      }
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    return Card(
      margin: const EdgeInsets.only(bottom: 8),
      child: ListTile(
        leading: const Icon(Icons.insert_drive_file_outlined),
        title: Text(widget.recording.relativePath),
        subtitle: Text(
          '${formatBytes(widget.recording.sizeBytes)} · '
          '${formatDateTime(widget.recording.receivedAt)}',
        ),
        trailing: _busy
            ? const SizedBox(
                height: 20,
                width: 20,
                child: CircularProgressIndicator(strokeWidth: 2),
              )
            : IconButton(
                onPressed: _download,
                icon: const Icon(Icons.download),
                tooltip: 'Download',
              ),
      ),
    );
  }
}

class _Notice extends StatelessWidget {
  const _Notice({required this.icon, required this.message});

  final IconData icon;
  final String message;

  @override
  Widget build(BuildContext context) {
    final colours = Theme.of(context).colorScheme;
    return Container(
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        color: colours.tertiaryContainer,
        borderRadius: BorderRadius.circular(8),
      ),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Icon(icon, size: 20, color: colours.onTertiaryContainer),
          const SizedBox(width: 12),
          Expanded(
            child: Text(
              message,
              style: Theme.of(context).textTheme.bodySmall
                  ?.copyWith(color: colours.onTertiaryContainer),
            ),
          ),
        ],
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
      padding: const EdgeInsets.symmetric(vertical: 3),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          SizedBox(
            width: 120,
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
