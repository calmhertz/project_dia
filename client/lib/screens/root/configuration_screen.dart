import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../../models/models.dart';
import '../../state/session.dart';
import '../../widgets/formatting.dart';
import '../../widgets/states.dart';
import '../satellites_screen.dart' show describeError;

class ConfigurationScreen extends StatefulWidget {
  const ConfigurationScreen({super.key});

  @override
  State<ConfigurationScreen> createState() => _ConfigurationScreenState();
}

class _ConfigurationScreenState extends State<ConfigurationScreen> {
  late Future<({StationConfig config, SchedulingSettings scheduling})> _future;

  @override
  void initState() {
    super.initState();
    _load();
  }

  void _load() {
    final api = context.read<Session>().api;
    setState(() {
      _future = () async {
        final config = await api.stationConfig();
        final scheduling = await api.schedulingConfig();
        return (config: config, scheduling: scheduling);
      }();
    });
  }

  void _report(String message) {
    if (!mounted) return;
    ScaffoldMessenger.of(context)
        .showSnackBar(SnackBar(content: Text(message)));
  }

  Future<bool> _apply(Future<String> Function() change) async {
    try {
      _report(await change());
      _load();
      return true;
    } catch (error) {
      _report(describeError(error));
      return false;
    }
  }

  String _committedNote(int upcoming) => upcoming == 0
      ? 'Saved.'
      : 'Saved. $upcoming scheduled pass${upcoming == 1 ? '' : 'es'} '
            'kept the previous settings.';

  @override
  Widget build(BuildContext context) {
    return FutureBuilder<
      ({StationConfig config, SchedulingSettings scheduling})
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

        final config = snapshot.data!.config;
        final scheduling = snapshot.data!.scheduling;
        final api = context.read<Session>().api;

        return ListView(
          padding: const EdgeInsets.all(16),
          children: [
            if (config.upcomingPasses > 0)
              _Notice(
                message:
                    '${config.upcomingPasses} pass'
                    '${config.upcomingPasses == 1 ? ' is' : 'es are'} already '
                    'scheduled. Changes here apply to future passes; those '
                    'keep the settings they were accepted under.',
              ),

            _StationSection(
              config: config,
              onSave: (changes) => _apply(() async {
                final upcoming = await api.updateStation(changes);
                return _committedNote(upcoming);
              }),
            ),
            const SizedBox(height: 24),

            for (final band in config.bands) ...[
              _BandSection(
                band: band,
                isActive: band.band == config.activeRfBand,
                onSave: (changes) => _apply(() async {
                  await api.updateBand(band.band, changes);
                  return '${band.band.toUpperCase()} settings saved.';
                }),
              ),
              const SizedBox(height: 24),
            ],

            _HardwareSection(
              hardware: config.hardware,
              onSave: (changes) => _apply(() async {
                final upcoming = await api.updateHardware(changes);
                return _committedNote(upcoming);
              }),
            ),
            const SizedBox(height: 24),

            _SchedulingSection(
              settings: scheduling,
              onSave: (changes) => _apply(() async {
                final effective = await api.updateSchedulingConfig(changes);
                return 'Saved. In force from ${formatDateTime(effective)}.';
              }),
            ),
            const SizedBox(height: 32),
          ],
        );
      },
    );
  }
}

class _Notice extends StatelessWidget {
  const _Notice({required this.message});

  final String message;

  @override
  Widget build(BuildContext context) {
    final colours = Theme.of(context).colorScheme;
    return Card(
      color: colours.secondaryContainer,
      margin: const EdgeInsets.only(bottom: 16),
      child: Padding(
        padding: const EdgeInsets.all(16),
        child: Row(
          children: [
            Icon(Icons.info_outline, color: colours.onSecondaryContainer),
            const SizedBox(width: 12),
            Expanded(
              child: Text(
                message,
                style: Theme.of(context).textTheme.bodyMedium
                    ?.copyWith(color: colours.onSecondaryContainer),
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class _Section extends StatefulWidget {
  const _Section({
    required this.title,
    required this.explanation,
    required this.fields,
    required this.onSave,
    required this.saveLabel,
  });

  final String title;
  final String explanation;
  final List<Widget> fields;

  final Future<bool> Function() onSave;

  final String saveLabel;

  @override
  State<_Section> createState() => _SectionState();
}

class _SectionState extends State<_Section> {
  bool _busy = false;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Card(
      child: Padding(
        padding: const EdgeInsets.all(16),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(widget.title, style: theme.textTheme.titleMedium),
            const SizedBox(height: 4),
            Text(
              widget.explanation,
              style: theme.textTheme.bodySmall?.copyWith(
                color: theme.colorScheme.onSurfaceVariant,
              ),
            ),
            const SizedBox(height: 16),
            ...widget.fields,
            const SizedBox(height: 16),
            Align(
              alignment: Alignment.centerRight,
              child: FilledButton(
                onPressed: _busy
                    ? null
                    : () async {
                        setState(() => _busy = true);
                        await widget.onSave();
                        if (mounted) setState(() => _busy = false);
                      },
                child: _busy
                    ? const SizedBox(
                        height: 16,
                        width: 16,
                        child: CircularProgressIndicator(strokeWidth: 2),
                      )
                    : Text(widget.saveLabel),
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class _StationSection extends StatefulWidget {
  const _StationSection({required this.config, required this.onSave});

  final StationConfig config;
  final Future<bool> Function(Map<String, dynamic>) onSave;

  @override
  State<_StationSection> createState() => _StationSectionState();
}

class _StationSectionState extends State<_StationSection> {
  late final TextEditingController _name;
  late final TextEditingController _latitude;
  late final TextEditingController _longitude;
  late final TextEditingController _altitude;
  late final TextEditingController _timezone;
  late String _band;

  @override
  void initState() {
    super.initState();
    _name = TextEditingController(text: widget.config.name);
    _latitude = TextEditingController(
      text: widget.config.latitudeDegrees.toString(),
    );
    _longitude = TextEditingController(
      text: widget.config.longitudeDegrees.toString(),
    );
    _altitude = TextEditingController(
      text: widget.config.altitudeM.toStringAsFixed(0),
    );
    _timezone = TextEditingController(text: widget.config.timezone);
    _band = widget.config.activeRfBand;
  }

  @override
  void dispose() {
    _name.dispose();
    _latitude.dispose();
    _longitude.dispose();
    _altitude.dispose();
    _timezone.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return _Section(
      title: 'Station',
      explanation:
          'Location drives every prediction. The active band decides what '
          'the next pass records.',
      saveLabel: 'Save station',
      fields: [
        _Field(controller: _name, label: 'Name'),
        const SizedBox(height: 12),
        Row(
          children: [
            Expanded(
              child: _Field(
                controller: _latitude,
                label: 'Latitude',
                numeric: true,
              ),
            ),
            const SizedBox(width: 12),
            Expanded(
              child: _Field(
                controller: _longitude,
                label: 'Longitude',
                numeric: true,
              ),
            ),
          ],
        ),
        const SizedBox(height: 12),
        Row(
          children: [
            Expanded(
              child: _Field(
                controller: _altitude,
                label: 'Altitude (m)',
                numeric: true,
              ),
            ),
            const SizedBox(width: 12),
            Expanded(
              child: _Field(controller: _timezone, label: 'Timezone'),
            ),
          ],
        ),
        const SizedBox(height: 16),
        SegmentedButton<String>(
          segments: const [
            ButtonSegment(value: 'vhf', label: Text('VHF')),
            ButtonSegment(value: 'uhf', label: Text('UHF')),
          ],
          selected: {_band},
          onSelectionChanged: (value) => setState(() => _band = value.first),
        ),
      ],
      onSave: () => widget.onSave({
        'name': _name.text.trim(),
        'latitude_degrees': double.tryParse(_latitude.text) ?? 0,
        'longitude_degrees': double.tryParse(_longitude.text) ?? 0,
        'altitude_m': double.tryParse(_altitude.text) ?? 0,
        'timezone': _timezone.text.trim(),
        'active_rf_band': _band,
      }),
    );
  }
}

class _BandSection extends StatefulWidget {
  const _BandSection({
    required this.band,
    required this.isActive,
    required this.onSave,
  });

  final BandConfig band;
  final bool isActive;
  final Future<bool> Function(Map<String, dynamic>) onSave;

  @override
  State<_BandSection> createState() => _BandSectionState();
}

class _BandSectionState extends State<_BandSection> {
  late final TextEditingController _antenna;
  late final TextEditingController _frequency;
  late final TextEditingController _sampleRate;
  late final TextEditingController _gain;
  late final TextEditingController _ppm;
  late bool _biasTee;

  @override
  void initState() {
    super.initState();
    _antenna = TextEditingController(text: widget.band.antennaDescription);
    _frequency = TextEditingController(
      text: widget.band.centerFrequencyHz?.toString() ?? '',
    );
    _sampleRate = TextEditingController(
      text: widget.band.sampleRateHz?.toString() ?? '',
    );
    _gain = TextEditingController(text: widget.band.gainDb?.toString() ?? '');
    _ppm = TextEditingController(text: widget.band.ppmCorrection.toString());
    _biasTee = widget.band.biasTeeEnabled;
  }

  @override
  void dispose() {
    _antenna.dispose();
    _frequency.dispose();
    _sampleRate.dispose();
    _gain.dispose();
    _ppm.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return _Section(
      title:
          '${widget.band.band.toUpperCase()} antenna'
          '${widget.isActive ? ' (active)' : ''}',
      explanation: widget.band.centerFrequencyHz == null
          ? 'This band has no centre frequency, so passes on it cannot be '
                'approved. Set one here.'
          : 'Applies to passes planned after this change.',
      saveLabel: 'Save ${widget.band.band.toUpperCase()}',
      fields: [
        _Field(controller: _antenna, label: 'Antenna'),
        const SizedBox(height: 12),
        _Field(
          controller: _frequency,
          label: 'Centre frequency (Hz)',
          numeric: true,
        ),
        const SizedBox(height: 12),
        Row(
          children: [
            Expanded(
              child: _Field(
                controller: _sampleRate,
                label: 'Sample rate (Hz)',
                numeric: true,
              ),
            ),
            const SizedBox(width: 12),
            Expanded(
              child: _Field(
                controller: _gain,
                label: 'Gain (dB)',
                numeric: true,
              ),
            ),
          ],
        ),
        const SizedBox(height: 12),
        _Field(controller: _ppm, label: 'PPM correction', numeric: true),
        SwitchListTile(
          contentPadding: EdgeInsets.zero,
          value: _biasTee,
          onChanged: (value) => setState(() => _biasTee = value),
          title: const Text('Bias tee'),
          subtitle: const Text('Power a mast-head amplifier over the coax.'),
        ),
      ],
      onSave: () {
        final changes = <String, dynamic>{
          'antenna_description': _antenna.text.trim(),
          'ppm_correction': int.tryParse(_ppm.text) ?? 0,
          'bias_tee_enabled': _biasTee,
        };
        final frequency = int.tryParse(_frequency.text);
        if (frequency != null) changes['center_frequency_hz'] = frequency;
        final rate = int.tryParse(_sampleRate.text);
        if (rate != null) changes['sample_rate_hz'] = rate;
        final gain = double.tryParse(_gain.text);
        if (gain != null) changes['gain_db'] = gain;
        return widget.onSave(changes);
      },
    );
  }
}

class _HardwareSection extends StatefulWidget {
  const _HardwareSection({required this.hardware, required this.onSave});

  final HardwareConfig hardware;
  final Future<bool> Function(Map<String, dynamic>) onSave;

  @override
  State<_HardwareSection> createState() => _HardwareSectionState();
}

class _HardwareSectionState extends State<_HardwareSection> {
  late final TextEditingController _port;
  late final TextEditingController _baud;
  late final TextEditingController _azimuth;
  late final TextEditingController _elevation;
  late final TextEditingController _sdr;
  String? _error;

  @override
  void initState() {
    super.initState();
    _port = TextEditingController(text: widget.hardware.rotatorSerialPort);
    _baud = TextEditingController(
      text: widget.hardware.rotatorBaudRate.toString(),
    );
    _azimuth = TextEditingController(
      text: widget.hardware.parkAzimuthDegrees.toString(),
    );
    _elevation = TextEditingController(
      text: widget.hardware.parkElevationDegrees.toString(),
    );
    _sdr = TextEditingController(text: widget.hardware.sdrDeviceIdentifier);
  }

  @override
  void dispose() {
    _port.dispose();
    _baud.dispose();
    _azimuth.dispose();
    _elevation.dispose();
    _sdr.dispose();
    super.dispose();
  }

  String? _validate(int azimuth, int elevation) {
    if (azimuth < 0 || azimuth > 359) {
      return 'Park azimuth must be between 0 and 359 degrees.';
    }
    if (elevation < 0 || elevation > 90) {
      return 'Park elevation must be between 0 and 90 degrees.';
    }
    return null;
  }

  @override
  Widget build(BuildContext context) {
    return _Section(
      title: 'Rotator and receiver',
      explanation:
          'The park position is where the antenna rests between passes.',
      saveLabel: 'Save hardware',
      fields: [
        Row(
          children: [
            Expanded(
              child: _Field(controller: _port, label: 'Serial port'),
            ),
            const SizedBox(width: 12),
            Expanded(
              child: _Field(
                controller: _baud,
                label: 'Baud rate',
                numeric: true,
              ),
            ),
          ],
        ),
        const SizedBox(height: 12),
        Row(
          children: [
            Expanded(
              child: _Field(
                controller: _azimuth,
                label: 'Park azimuth (0-359)',
                numeric: true,
              ),
            ),
            const SizedBox(width: 12),
            Expanded(
              child: _Field(
                controller: _elevation,
                label: 'Park elevation (0-90)',
                numeric: true,
              ),
            ),
          ],
        ),
        const SizedBox(height: 12),
        _Field(controller: _sdr, label: 'Receiver device'),
        if (_error != null) ...[
          const SizedBox(height: 12),
          Text(
            _error!,
            style: TextStyle(color: Theme.of(context).colorScheme.error),
          ),
        ],
      ],
      onSave: () async {
        final azimuth = int.tryParse(_azimuth.text) ?? -1;
        final elevation = int.tryParse(_elevation.text) ?? -1;
        final problem = _validate(azimuth, elevation);
        setState(() => _error = problem);
        if (problem != null) return false;

        return widget.onSave({
          'rotator_serial_port': _port.text.trim(),
          'rotator_baud_rate': int.tryParse(_baud.text) ?? 0,
          'rotator_park_azimuth_degrees': azimuth,
          'rotator_park_elevation_degrees': elevation,
          'sdr_device_identifier': _sdr.text.trim(),
        });
      },
    );
  }
}

class _SchedulingSection extends StatefulWidget {
  const _SchedulingSection({required this.settings, required this.onSave});

  final SchedulingSettings settings;
  final Future<bool> Function(Map<String, dynamic>) onSave;

  @override
  State<_SchedulingSection> createState() => _SchedulingSectionState();
}

class _SchedulingSectionState extends State<_SchedulingSection> {
  late final TextEditingController _leadTime;
  late final TextEditingController _preBuffer;
  late final TextEditingController _postBuffer;
  late final TextEditingController _preRoll;
  late final TextEditingController _postRoll;
  late final TextEditingController _elevation;

  @override
  void initState() {
    super.initState();
    final settings = widget.settings;
    _leadTime = TextEditingController(
      text: settings.minimumLeadTime.inSeconds.toString(),
    );
    _preBuffer = TextEditingController(
      text: settings.prePassBuffer.inSeconds.toString(),
    );
    _postBuffer = TextEditingController(
      text: settings.postPassBuffer.inSeconds.toString(),
    );
    _preRoll = TextEditingController(
      text: settings.recordingPreRoll.inSeconds.toString(),
    );
    _postRoll = TextEditingController(
      text: settings.recordingPostRoll.inSeconds.toString(),
    );
    _elevation = TextEditingController(
      text: settings.minimumElevationDegrees.toString(),
    );
  }

  @override
  void dispose() {
    _leadTime.dispose();
    _preBuffer.dispose();
    _postBuffer.dispose();
    _preRoll.dispose();
    _postRoll.dispose();
    _elevation.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return _Section(
      title: 'Scheduling rules',
      explanation:
          'A new version takes effect after the new lead time has passed, so '
          'shortening it cannot be used to book something sooner than the '
          'old rule allowed.',
      saveLabel: 'Save scheduling',
      fields: [
        if (widget.settings.pending != null) ...[
          _PendingChange(pending: widget.settings.pending!),
          const SizedBox(height: 16),
        ],
        _Field(
          controller: _leadTime,
          label: 'Minimum lead time (seconds)',
          numeric: true,
        ),
        const SizedBox(height: 12),
        Row(
          children: [
            Expanded(
              child: _Field(
                controller: _preBuffer,
                label: 'Buffer before (s)',
                numeric: true,
              ),
            ),
            const SizedBox(width: 12),
            Expanded(
              child: _Field(
                controller: _postBuffer,
                label: 'Buffer after (s)',
                numeric: true,
              ),
            ),
          ],
        ),
        const SizedBox(height: 12),
        Row(
          children: [
            Expanded(
              child: _Field(
                controller: _preRoll,
                label: 'Record from (s before)',
                numeric: true,
              ),
            ),
            const SizedBox(width: 12),
            Expanded(
              child: _Field(
                controller: _postRoll,
                label: 'Record to (s after)',
                numeric: true,
              ),
            ),
          ],
        ),
        const SizedBox(height: 12),
        _Field(
          controller: _elevation,
          label: 'Minimum elevation (degrees)',
          numeric: true,
        ),
      ],
      onSave: () => widget.onSave({
        'minimum_lead_time_seconds': int.tryParse(_leadTime.text) ?? 0,
        'pre_pass_buffer_seconds': int.tryParse(_preBuffer.text) ?? 0,
        'post_pass_buffer_seconds': int.tryParse(_postBuffer.text) ?? 0,
        'recording_pre_roll_seconds': int.tryParse(_preRoll.text) ?? 0,
        'recording_post_roll_seconds': int.tryParse(_postRoll.text) ?? 0,
        'minimum_elevation_degrees': double.tryParse(_elevation.text) ?? 0,
      }),
    );
  }
}

class _Field extends StatelessWidget {
  const _Field({
    required this.controller,
    required this.label,
    this.numeric = false,
  });

  final TextEditingController controller;
  final String label;
  final bool numeric;

  @override
  Widget build(BuildContext context) {
    return TextField(
      controller: controller,
      keyboardType: numeric
          ? const TextInputType.numberWithOptions(decimal: true, signed: true)
          : TextInputType.text,
      decoration: InputDecoration(
        labelText: label,
        border: const OutlineInputBorder(),
        isDense: true,
      ),
    );
  }
}

class _PendingChange extends StatelessWidget {
  const _PendingChange({required this.pending});

  final SchedulingSettings pending;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final colours = theme.colorScheme;

    return Container(
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        color: colours.secondaryContainer,
        borderRadius: BorderRadius.circular(12),
      ),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Icon(Icons.schedule_send, color: colours.onSecondaryContainer),
          const SizedBox(width: 12),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  'A change is saved and waiting',
                  style: theme.textTheme.titleSmall?.copyWith(
                    color: colours.onSecondaryContainer,
                  ),
                ),
                const SizedBox(height: 4),
                Text(
                  'Lead time '
                  '${formatDuration(pending.minimumLeadTime)}, minimum '
                  'elevation '
                  '${pending.minimumElevationDegrees.toStringAsFixed(0)} '
                  'degrees, in force from '
                  '${formatDateTime(pending.effectiveFrom)} '
                  '(${formatRelative(pending.effectiveFrom)}). The values '
                  'below are the ones in force until then.',
                  style: theme.textTheme.bodySmall?.copyWith(
                    color: colours.onSecondaryContainer,
                  ),
                ),
              ],
            ),
          ),
        ],
      ),
    );
  }
}
