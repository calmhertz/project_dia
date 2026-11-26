import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../models/models.dart';
import '../state/session.dart';

/// Picks the station's IANA time zone from the list the Server reads out of
/// the host's tz database.
///
/// Typing filters the list, so "kolkata", "london" or "+05:30" all find their
/// zone. If the Server cannot offer a list, an IANA name can be typed instead
/// and the Server checks it.
class TimezoneDropdown extends StatefulWidget {
  const TimezoneDropdown({
    super.key,
    required this.value,
    required this.onChanged,
    this.enabled = true,
  });

  final String value;
  final ValueChanged<String> onChanged;
  final bool enabled;

  static const label = 'Timezone';

  @override
  State<TimezoneDropdown> createState() => _TimezoneDropdownState();
}

class _TimezoneDropdownState extends State<TimezoneDropdown> {
  late Future<List<TimeZoneOption>> _zones;
  final _typed = TextEditingController();

  @override
  void initState() {
    super.initState();
    _typed.text = widget.value;
    _zones = context.read<Session>().api.timezones();
  }

  @override
  void dispose() {
    _typed.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return FutureBuilder<List<TimeZoneOption>>(
      future: _zones,
      builder: (context, snapshot) {
        if (snapshot.connectionState != ConnectionState.done) {
          return const TextField(
            enabled: false,
            decoration: InputDecoration(
              labelText: TimezoneDropdown.label,
              hintText: 'Loading time zones…',
              border: OutlineInputBorder(),
            ),
          );
        }
        final zones = snapshot.data;
        if (snapshot.hasError || zones == null || zones.isEmpty) {
          return _typedFallback();
        }
        return _dropdown(zones);
      },
    );
  }

  Widget _dropdown(List<TimeZoneOption> zones) {
    final entries = [
      // A stored zone the list does not carry (an alias such as
      // Asia/Calcutta) is still shown, so it is never silently replaced.
      if (widget.value.isNotEmpty &&
          !zones.any((zone) => zone.name == widget.value))
        DropdownMenuEntry(value: widget.value, label: widget.value),
      for (final zone in zones)
        DropdownMenuEntry(value: zone.name, label: zone.label),
    ];
    return DropdownMenu<String>(
      key: const ValueKey('timezone-dropdown'),
      enabled: widget.enabled,
      initialSelection: widget.value.isEmpty ? null : widget.value,
      label: const Text(TimezoneDropdown.label),
      helperText: 'Search by city or offset, e.g. Kolkata or +05:30',
      expandedInsets: EdgeInsets.zero,
      menuHeight: 320,
      enableFilter: true,
      requestFocusOnTap: true,
      leadingIcon: const Icon(Icons.public),
      dropdownMenuEntries: entries,
      onSelected: (name) {
        if (name != null) widget.onChanged(name);
      },
    );
  }

  Widget _typedFallback() {
    return TextFormField(
      controller: _typed,
      enabled: widget.enabled,
      decoration: const InputDecoration(
        labelText: TimezoneDropdown.label,
        helperText: 'Enter an IANA name such as Asia/Kolkata or UTC',
        border: OutlineInputBorder(),
      ),
      validator: (value) {
        final name = (value ?? '').trim();
        if (name == 'UTC' || name.contains('/')) return null;
        return 'Use an IANA name such as Asia/Kolkata, not IST or +05:30.';
      },
      onChanged: (value) => widget.onChanged(value.trim()),
    );
  }
}
