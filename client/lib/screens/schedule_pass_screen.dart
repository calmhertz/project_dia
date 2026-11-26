import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../api/api_exception.dart';
import '../models/models.dart';
import '../state/session.dart';
import '../widgets/formatting.dart';
import '../widgets/pipeline_upload.dart';
import 'satellites_screen.dart' show describeError;

class SchedulePassScreen extends StatefulWidget {
  const SchedulePassScreen({
    super.key,
    required this.satellite,
    required this.pass,
    required this.stationBand,
  });

  final Satellite satellite;
  final PredictedPass pass;
  final String stationBand;

  @override
  State<SchedulePassScreen> createState() => _SchedulePassScreenState();
}

class _SchedulePassScreenState extends State<SchedulePassScreen> {
  late String _band = widget.stationBand.isEmpty ? 'vhf' : widget.stationBand;
  String _recordingMode = 'raw';
  bool _isPublic = false;
  bool _busy = false;
  String? _pipelineId;
  late Future<PipelineCatalogue> _pipelines;

  ApiException? _failure;

  @override
  void initState() {
    super.initState();
    _pipelines = context.read<Session>().api.pipelines()..ignore();
  }

  Future<void> _useUploadedPipeline() async {
    final uploaded = await UploadPipelineDialog.show(context);
    if (uploaded == null || !mounted) return;
    setState(() {
      _pipelines = context.read<Session>().api.pipelines()..ignore();
      _pipelineId = uploaded.id;
    });
  }

  bool get _needsPipeline =>
      _recordingMode == 'process' || _recordingMode == 'raw_and_process';

  bool get _bandMismatch =>
      widget.stationBand.isNotEmpty && _band != widget.stationBand;

  Future<void> _submit({bool override = false}) async {
    setState(() {
      _busy = true;
      _failure = null;
    });

    try {
      final result = await context.read<Session>().api.requestPass(
        satelliteId: widget.satellite.id,
        aos: widget.pass.aos,
        band: _band,
        recordingMode: _recordingMode,
        isPublic: _isPublic,
        pipelineId: _needsPipeline ? _pipelineId : null,
        override: override,
      );
      if (!mounted) return;
      Navigator.of(context).pop(result);
    } on ApiException catch (error) {
      if (mounted) setState(() => _failure = error);
    } on NetworkException catch (error) {
      if (mounted) {
        setState(
          () => _failure = ApiException(
            statusCode: 0,
            code: 'network',
            message: error.message,
          ),
        );
      }
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _confirmOverride() async {
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('Take this slot?'),
        content: const Text(
          'The pass already holding this time will be cancelled, and its '
          'owner will be told why. This cannot be undone.',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context, false),
            child: const Text('Leave it'),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(context, true),
            child: const Text('Override'),
          ),
        ],
      ),
    );
    if (confirmed == true) await _submit(override: true);
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);

    return Scaffold(
      appBar: AppBar(title: const Text('Request this pass')),
      body: ListView(
        padding: const EdgeInsets.all(16),
        children: [
          _PassSummary(satellite: widget.satellite, pass: widget.pass),
          const SizedBox(height: 24),

          Text('Band', style: theme.textTheme.titleSmall),
          const SizedBox(height: 8),
          SegmentedButton<String>(
            segments: const [
              ButtonSegment(value: 'vhf', label: Text('VHF')),
              ButtonSegment(value: 'uhf', label: Text('UHF')),
            ],
            selected: {_band},
            onSelectionChanged: (selection) =>
                setState(() => _band = selection.first),
          ),
          if (_bandMismatch) ...[
            const SizedBox(height: 12),
            _WarningCard(
              message:
                  'The station is currently configured for '
                  '${widget.stationBand.toUpperCase()}, while this pass requires '
                  '${_band.toUpperCase()}. Reception may be poor unless the '
                  'station configuration is changed before execution.',
            ),
          ],

          const SizedBox(height: 24),
          Text('What to produce', style: theme.textTheme.titleSmall),
          const SizedBox(height: 8),
          RadioGroup<String>(
            groupValue: _recordingMode,
            onChanged: (value) => setState(() => _recordingMode = value!),
            child: const Column(
              children: [
                RadioListTile<String>(
                  value: 'raw',
                  title: Text('Raw recording'),
                  subtitle: Text('Capture the signal for processing later.'),
                ),
                RadioListTile<String>(
                  value: 'process',
                  title: Text('Processed result'),
                  subtitle: Text('Decode during the pass using a pipeline.'),
                ),
              ],
            ),
          ),

          if (_needsPipeline) ...[
            const SizedBox(height: 16),
            _PipelineChoice(
              future: _pipelines,
              selected: _pipelineId,
              onSelected: (value) => setState(() => _pipelineId = value),
              onUploaded: _useUploadedPipeline,
            ),
          ],

          const SizedBox(height: 8),
          SwitchListTile(
            value: _isPublic,
            onChanged: (value) => setState(() => _isPublic = value),
            title: const Text('Share publicly'),
            subtitle: const Text(
              'Public passes and their recordings are visible to all users.',
            ),
          ),

          if (_failure != null) ...[
            const SizedBox(height: 16),
            _FailureCard(failure: _failure!),
            if (_failure!.isStationConflict &&
                (context.watch<Session>().user?.isRoot ?? false)) ...[
              const SizedBox(height: 12),
              OutlinedButton.icon(
                onPressed: _busy ? null : _confirmOverride,
                icon: const Icon(Icons.swap_horiz),
                label: const Text('Override and take this slot'),
                style: OutlinedButton.styleFrom(
                  foregroundColor: theme.colorScheme.error,
                ),
              ),
            ],
          ],

          const SizedBox(height: 24),
          FilledButton(
            onPressed: (_busy || (_needsPipeline && _pipelineId == null))
                ? null
                : _submit,
            style: FilledButton.styleFrom(
              padding: const EdgeInsets.symmetric(vertical: 16),
            ),
            child: _busy
                ? const SizedBox(
                    height: 20,
                    width: 20,
                    child: CircularProgressIndicator(strokeWidth: 2),
                  )
                : const Text('Request pass'),
          ),
          const SizedBox(height: 8),
          Text(
            'Your request goes to an operator for approval.',
            textAlign: TextAlign.center,
            style: theme.textTheme.bodySmall?.copyWith(
              color: theme.colorScheme.onSurfaceVariant,
            ),
          ),
        ],
      ),
    );
  }
}

class _PassSummary extends StatelessWidget {
  const _PassSummary({required this.satellite, required this.pass});

  final Satellite satellite;
  final PredictedPass pass;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Card(
      child: Padding(
        padding: const EdgeInsets.all(16),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(satellite.name, style: theme.textTheme.titleMedium),
            const SizedBox(height: 12),
            _Row(
              'Starts',
              '${formatDateTime(pass.aos)}  '
                  '(${formatRelative(pass.aos)})',
            ),
            _Row('Ends', formatTime(pass.los)),
            _Row('Duration', formatDuration(pass.duration)),
            _Row(
              'Highest point',
              '${pass.maxElevationDegrees.toStringAsFixed(0)}° elevation',
            ),
            _Row(
              'Rises',
              '${pass.aosAzimuthDegrees.toStringAsFixed(0)}° azimuth',
            ),
            _Row(
              'Sets',
              '${pass.losAzimuthDegrees.toStringAsFixed(0)}° azimuth',
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
      padding: const EdgeInsets.symmetric(vertical: 3),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          SizedBox(
            width: 110,
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

class _WarningCard extends StatelessWidget {
  const _WarningCard({required this.message});

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
          Icon(
            Icons.warning_amber_outlined,
            size: 20,
            color: colours.onTertiaryContainer,
          ),
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

class _FailureCard extends StatelessWidget {
  const _FailureCard({required this.failure});

  final ApiException failure;

  @override
  Widget build(BuildContext context) {
    final colours = Theme.of(context).colorScheme;

    String title;
    String detail;
    IconData icon;

    if (failure.isStationConflict) {
      icon = Icons.event_busy;
      title = 'This time is already occupied.';
      final from = failure.details['occupied_from'] as String?;
      final to = failure.details['occupied_to'] as String?;
      detail = (from != null && to != null)
          ? 'The station is reserved from ${formatTime(DateTime.parse(from))} '
                'to ${formatTime(DateTime.parse(to))}. Choose another pass.'
          : 'Choose another pass.';
    } else if (failure.isWorkerOffline) {
      icon = Icons.cloud_off;
      title = 'The ground station is offline.';
      detail = 'New passes cannot be scheduled until it reconnects.';
    } else if (failure.isLeadTime) {
      icon = Icons.timer_off_outlined;
      title = 'This pass starts too soon.';
      detail = failure.message;
    } else {
      icon = Icons.error_outline;
      title = 'The request was not accepted.';
      detail = failure.message;
    }

    return Container(
      padding: const EdgeInsets.all(16),
      decoration: BoxDecoration(
        color: colours.errorContainer,
        borderRadius: BorderRadius.circular(12),
      ),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Icon(icon, color: colours.onErrorContainer),
          const SizedBox(width: 12),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  title,
                  style: Theme.of(context).textTheme.titleSmall
                      ?.copyWith(color: colours.onErrorContainer),
                ),
                const SizedBox(height: 4),
                Text(
                  detail,
                  style: Theme.of(context).textTheme.bodySmall
                      ?.copyWith(color: colours.onErrorContainer),
                ),
              ],
            ),
          ),
        ],
      ),
    );
  }
}

class _PipelineChoice extends StatelessWidget {
  const _PipelineChoice({
    required this.future,
    required this.selected,
    required this.onSelected,
    required this.onUploaded,
  });

  final Future<PipelineCatalogue> future;
  final String? selected;
  final ValueChanged<String?> onSelected;
  final Future<void> Function() onUploaded;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text('Pipeline', style: theme.textTheme.titleSmall),
        const SizedBox(height: 8),
        FutureBuilder<PipelineCatalogue>(
          future: future,
          builder: (context, snapshot) {
            if (snapshot.connectionState == ConnectionState.waiting) {
              return const LinearProgressIndicator();
            }
            if (snapshot.hasError) {
              return Text(
                describeError(snapshot.error),
                style: TextStyle(color: theme.colorScheme.error),
              );
            }

            final catalogue = snapshot.data!;
            if (catalogue.pipelines.isEmpty) {
              return Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    catalogue.reportedByStation
                        ? 'The station reported no pipelines.'
                        : 'The ground station has not reported its SatDump '
                              'installation yet.',
                    style: theme.textTheme.bodySmall?.copyWith(
                      color: theme.colorScheme.onSurfaceVariant,
                    ),
                  ),
                  const SizedBox(height: 8),
                  _UploadButton(onUploaded: onUploaded),
                ],
              );
            }

            return Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                DropdownButtonFormField<String>(
                  initialValue: selected,
                  isExpanded: true,
                  decoration: const InputDecoration(
                    border: OutlineInputBorder(),
                    helperText: 'What SatDump will run during the pass.',
                  ),
                  items: [
                    for (final pipeline in catalogue.pipelines)
                      DropdownMenuItem(
                        value: pipeline.id,
                        child: Text(
                          pipeline.isCustom
                              ? '${pipeline.name} (custom)'
                              : pipeline.name,
                          overflow: TextOverflow.ellipsis,
                        ),
                      ),
                  ],
                  onChanged: onSelected,
                ),
                const SizedBox(height: 8),
                _UploadButton(onUploaded: onUploaded),
              ],
            );
          },
        ),
      ],
    );
  }
}

class _UploadButton extends StatelessWidget {
  const _UploadButton({required this.onUploaded});

  final Future<void> Function() onUploaded;

  @override
  Widget build(BuildContext context) {
    return TextButton.icon(
      onPressed: onUploaded,
      icon: const Icon(Icons.upload_file, size: 18),
      label: const Text('Upload pipeline JSON'),
    );
  }
}
