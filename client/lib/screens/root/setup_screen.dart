import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../../state/session.dart';
import '../satellites_screen.dart' show describeError;

class SetupScreen extends StatefulWidget {
  const SetupScreen({super.key, required this.onCompleted});

  final VoidCallback onCompleted;

  @override
  State<SetupScreen> createState() => _SetupScreenState();
}

class _SetupScreenState extends State<SetupScreen> {
  final _formKey = GlobalKey<FormState>();

  final _stationName = TextEditingController();
  final _latitude = TextEditingController();
  final _longitude = TextEditingController();
  final _altitude = TextEditingController(text: '0');
  final _timezone = TextEditingController(text: 'UTC');
  final _vhfAntenna = TextEditingController(text: 'Turnstile');
  final _uhfAntenna = TextEditingController(text: 'Yagi');
  final _vhfFrequency = TextEditingController(text: '137100000');
  final _uhfFrequency = TextEditingController(text: '435000000');
  final _sampleRate = TextEditingController(text: '2048000');
  final _gain = TextEditingController(text: '40');
  final _rotatorPort = TextEditingController(text: '/dev/ttyUSB0');
  final _rotatorBaud = TextEditingController(text: '9600');
  final _parkAzimuth = TextEditingController(text: '0');
  final _parkElevation = TextEditingController(text: '0');
  final _sdrDevice = TextEditingController(text: 'rtlsdr');
  final _leadTime = TextEditingController(text: '1800');
  final _preBuffer = TextEditingController(text: '120');
  final _postBuffer = TextEditingController(text: '120');
  final _preRoll = TextEditingController(text: '10');
  final _postRoll = TextEditingController(text: '10');
  final _minimumElevation = TextEditingController(text: '10');
  final _workerName = TextEditingController(text: 'worker-1');

  String _activeBand = 'vhf';
  bool _busy = false;
  String? _failure;

  @override
  void dispose() {
    for (final controller in [
      _stationName,
      _latitude,
      _longitude,
      _altitude,
      _timezone,
      _vhfAntenna,
      _uhfAntenna,
      _sampleRate,
      _gain,
      _rotatorPort,
      _rotatorBaud,
      _parkAzimuth,
      _parkElevation,
      _sdrDevice,
      _leadTime,
      _preBuffer,
      _postBuffer,
      _preRoll,
      _postRoll,
      _minimumElevation,
      _workerName,
    ]) {
      controller.dispose();
    }
    super.dispose();
  }

  Future<void> _submit() async {
    if (!_formKey.currentState!.validate()) return;
    setState(() {
      _busy = true;
      _failure = null;
    });

    final sampleRate = int.tryParse(_sampleRate.text) ?? 2048000;
    final gain = double.tryParse(_gain.text) ?? 40;

    try {
      await context.read<Session>().api.runSetup({
        'station_name': _stationName.text.trim(),
        'latitude': double.parse(_latitude.text),
        'longitude': double.parse(_longitude.text),
        'altitude_m': double.tryParse(_altitude.text) ?? 0,
        'timezone': _timezone.text.trim(),
        'active_rf_band': _activeBand,
        'bands': [
          {
            'band': 'vhf',
            'antenna_description': _vhfAntenna.text.trim(),
            'center_frequency_hz': int.parse(_vhfFrequency.text.trim()),
            'sample_rate_hz': sampleRate,
            'gain_db': gain,
          },
          {
            'band': 'uhf',
            'antenna_description': _uhfAntenna.text.trim(),
            'center_frequency_hz': int.parse(_uhfFrequency.text.trim()),
            'sample_rate_hz': sampleRate,
            'gain_db': gain,
          },
        ],
        'rotator_serial_port': _rotatorPort.text.trim(),
        'rotator_baud_rate': int.tryParse(_rotatorBaud.text) ?? 9600,
        'rotator_park_azimuth_degrees': int.tryParse(_parkAzimuth.text) ?? 0,
        'rotator_park_elevation_degrees':
            int.tryParse(_parkElevation.text) ?? 0,
        'sdr_device_identifier': _sdrDevice.text.trim(),
        'minimum_lead_time_seconds': int.tryParse(_leadTime.text) ?? 1800,
        'pre_pass_buffer_seconds': int.tryParse(_preBuffer.text) ?? 120,
        'post_pass_buffer_seconds': int.tryParse(_postBuffer.text) ?? 120,
        'recording_pre_roll_seconds': int.tryParse(_preRoll.text) ?? 10,
        'recording_post_roll_seconds': int.tryParse(_postRoll.text) ?? 10,
        'minimum_elevation_degrees':
            double.tryParse(_minimumElevation.text) ?? 10,
        'worker_name': _workerName.text.trim(),
      });
      if (mounted) widget.onCompleted();
    } catch (error) {
      if (mounted) setState(() => _failure = describeError(error));
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  String? _required(String? value) =>
      (value == null || value.trim().isEmpty) ? 'Required' : null;

  String? _degrees(String? value, double limit) {
    final parsed = double.tryParse(value ?? '');
    if (parsed == null) return 'Enter a number';
    if (parsed < -limit || parsed > limit) return 'Between -$limit and $limit';
    return null;
  }

  String? _frequency(String? value) {
    final parsed = int.tryParse((value ?? '').trim());
    if (parsed == null) return 'Enter the frequency in hertz';
    if (parsed <= 0) return 'Must be positive';
    return null;
  }

  String? _park(String? value, int limit) {
    final parsed = int.tryParse(value ?? '');
    if (parsed == null) return 'Enter a whole number';
    if (parsed < 0 || parsed > limit) return 'Between 0 and $limit';
    return null;
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);

    return Scaffold(
      appBar: AppBar(title: const Text('Set up this station')),
      body: Form(
        key: _formKey,
        child: ListView(
          padding: const EdgeInsets.all(24),
          children: [
            Text(
              'This runs once. Everything here can be changed afterwards from '
              'the configuration screen.',
              style: theme.textTheme.bodyMedium?.copyWith(
                color: theme.colorScheme.onSurfaceVariant,
              ),
            ),
            const SizedBox(height: 24),

            _Group(
              title: 'Station',
              children: [
                TextFormField(
                  controller: _stationName,
                  decoration: const InputDecoration(
                    labelText: 'Station name',
                    border: OutlineInputBorder(),
                  ),
                  validator: _required,
                ),
                const SizedBox(height: 12),
                Row(
                  children: [
                    Expanded(
                      child: TextFormField(
                        controller: _latitude,
                        decoration: const InputDecoration(
                          labelText: 'Latitude',
                          border: OutlineInputBorder(),
                        ),
                        validator: (value) => _degrees(value, 90),
                      ),
                    ),
                    const SizedBox(width: 12),
                    Expanded(
                      child: TextFormField(
                        controller: _longitude,
                        decoration: const InputDecoration(
                          labelText: 'Longitude',
                          border: OutlineInputBorder(),
                        ),
                        validator: (value) => _degrees(value, 180),
                      ),
                    ),
                  ],
                ),
                const SizedBox(height: 12),
                Row(
                  children: [
                    Expanded(
                      child: _Plain(
                        controller: _altitude,
                        label: 'Altitude (m)',
                      ),
                    ),
                    const SizedBox(width: 12),
                    Expanded(
                      child: _Plain(controller: _timezone, label: 'Timezone'),
                    ),
                  ],
                ),
                const SizedBox(height: 16),
                Text('Active band', style: theme.textTheme.labelLarge),
                const SizedBox(height: 8),
                SegmentedButton<String>(
                  segments: const [
                    ButtonSegment(value: 'vhf', label: Text('VHF')),
                    ButtonSegment(value: 'uhf', label: Text('UHF')),
                  ],
                  selected: {_activeBand},
                  onSelectionChanged: (value) =>
                      setState(() => _activeBand = value.first),
                ),
              ],
            ),

            _Group(
              title: 'Antennas',
              children: [
                _Plain(controller: _vhfAntenna, label: 'VHF antenna'),
                const SizedBox(height: 12),
                TextFormField(
                  controller: _vhfFrequency,
                  decoration: const InputDecoration(
                    labelText: 'VHF centre frequency (Hz)',
                    helperText: '137100000 is the NOAA APT downlink.',
                    border: OutlineInputBorder(),
                  ),
                  validator: _frequency,
                ),
                const SizedBox(height: 12),
                _Plain(controller: _uhfAntenna, label: 'UHF antenna'),
                const SizedBox(height: 12),
                TextFormField(
                  controller: _uhfFrequency,
                  decoration: const InputDecoration(
                    labelText: 'UHF centre frequency (Hz)',
                    border: OutlineInputBorder(),
                  ),
                  validator: _frequency,
                ),
                const SizedBox(height: 12),
                Row(
                  children: [
                    Expanded(
                      child: _Plain(
                        controller: _sampleRate,
                        label: 'Sample rate (Hz)',
                      ),
                    ),
                    const SizedBox(width: 12),
                    Expanded(
                      child: _Plain(controller: _gain, label: 'Gain (dB)'),
                    ),
                  ],
                ),
              ],
            ),

            _Group(
              title: 'Rotator and receiver',
              children: [
                Row(
                  children: [
                    Expanded(
                      child: TextFormField(
                        controller: _rotatorPort,
                        decoration: const InputDecoration(
                          labelText: 'Rotator serial port',
                          border: OutlineInputBorder(),
                        ),
                        validator: _required,
                      ),
                    ),
                    const SizedBox(width: 12),
                    Expanded(
                      child: _Plain(
                        controller: _rotatorBaud,
                        label: 'Baud rate',
                      ),
                    ),
                  ],
                ),
                const SizedBox(height: 12),
                Row(
                  children: [
                    Expanded(
                      child: TextFormField(
                        controller: _parkAzimuth,
                        decoration: const InputDecoration(
                          labelText: 'Park azimuth (0-359)',
                          border: OutlineInputBorder(),
                        ),
                        validator: (value) => _park(value, 359),
                      ),
                    ),
                    const SizedBox(width: 12),
                    Expanded(
                      child: TextFormField(
                        controller: _parkElevation,
                        decoration: const InputDecoration(
                          labelText: 'Park elevation (0-90)',
                          border: OutlineInputBorder(),
                        ),
                        validator: (value) => _park(value, 90),
                      ),
                    ),
                  ],
                ),
                const SizedBox(height: 12),
                _Plain(controller: _sdrDevice, label: 'Receiver device'),
              ],
            ),

            _Group(
              title: 'Scheduling',
              children: [
                _Plain(
                  controller: _leadTime,
                  label: 'Minimum lead time (seconds)',
                ),
                const SizedBox(height: 12),
                Row(
                  children: [
                    Expanded(
                      child: _Plain(
                        controller: _preBuffer,
                        label: 'Buffer before (s)',
                      ),
                    ),
                    const SizedBox(width: 12),
                    Expanded(
                      child: _Plain(
                        controller: _postBuffer,
                        label: 'Buffer after (s)',
                      ),
                    ),
                  ],
                ),
                const SizedBox(height: 12),
                Row(
                  children: [
                    Expanded(
                      child: _Plain(
                        controller: _preRoll,
                        label: 'Record from (s before)',
                      ),
                    ),
                    const SizedBox(width: 12),
                    Expanded(
                      child: _Plain(
                        controller: _postRoll,
                        label: 'Record to (s after)',
                      ),
                    ),
                  ],
                ),
                const SizedBox(height: 12),
                _Plain(
                  controller: _minimumElevation,
                  label: 'Minimum elevation (degrees)',
                ),
              ],
            ),

            _Group(
              title: 'Ground station',
              children: [
                TextFormField(
                  controller: _workerName,
                  decoration: const InputDecoration(
                    labelText: 'Ground station name',
                    helperText:
                        'The name the station software registers under.',
                    border: OutlineInputBorder(),
                  ),
                  validator: _required,
                ),
              ],
            ),

            if (_failure != null) ...[
              const SizedBox(height: 8),
              Card(
                color: theme.colorScheme.errorContainer,
                child: Padding(
                  padding: const EdgeInsets.all(16),
                  child: Row(
                    children: [
                      Icon(
                        Icons.error_outline,
                        color: theme.colorScheme.onErrorContainer,
                      ),
                      const SizedBox(width: 12),
                      Expanded(
                        child: Text(
                          _failure!,
                          style: TextStyle(
                            color: theme.colorScheme.onErrorContainer,
                          ),
                        ),
                      ),
                    ],
                  ),
                ),
              ),
            ],

            const SizedBox(height: 24),
            FilledButton(
              onPressed: _busy ? null : _submit,
              style: FilledButton.styleFrom(
                padding: const EdgeInsets.symmetric(vertical: 16),
              ),
              child: _busy
                  ? const SizedBox(
                      height: 20,
                      width: 20,
                      child: CircularProgressIndicator(strokeWidth: 2),
                    )
                  : const Text('Complete setup'),
            ),
            const SizedBox(height: 32),
          ],
        ),
      ),
    );
  }
}

class _Group extends StatelessWidget {
  const _Group({required this.title, required this.children});

  final String title;
  final List<Widget> children;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.only(bottom: 24),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(title, style: Theme.of(context).textTheme.titleMedium),
          const SizedBox(height: 12),
          ...children,
        ],
      ),
    );
  }
}

class _Plain extends StatelessWidget {
  const _Plain({required this.controller, required this.label});

  final TextEditingController controller;
  final String label;

  @override
  Widget build(BuildContext context) {
    return TextFormField(
      controller: controller,
      decoration: InputDecoration(
        labelText: label,
        border: const OutlineInputBorder(),
      ),
    );
  }
}
