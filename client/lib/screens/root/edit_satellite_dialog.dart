import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../../models/models.dart';
import '../../state/session.dart';
import '../satellites_screen.dart' show describeError;

/// Edit a catalogue entry, Root only.
///
/// The catalog number is not editable: it is the satellite's identity and the
/// key every orbital-data lookup uses. Closing a satellite for scheduling is
/// the reversible alternative to removing it.
class EditSatelliteDialog extends StatefulWidget {
  const EditSatelliteDialog({super.key, required this.satellite});

  final Satellite satellite;

  @override
  State<EditSatelliteDialog> createState() => _EditSatelliteDialogState();
}

class _EditSatelliteDialogState extends State<EditSatelliteDialog> {
  late final TextEditingController _name;
  late final TextEditingController _description;
  late bool _schedulable;
  bool _busy = false;
  String? _failure;

  @override
  void initState() {
    super.initState();
    _name = TextEditingController(text: widget.satellite.name);
    _description = TextEditingController(text: widget.satellite.description);
    _schedulable = widget.satellite.isSchedulable;
  }

  @override
  void dispose() {
    _name.dispose();
    _description.dispose();
    super.dispose();
  }

  Future<void> _submit() async {
    if (_name.text.trim().isEmpty) {
      setState(() => _failure = 'A satellite needs a name.');
      return;
    }
    setState(() {
      _busy = true;
      _failure = null;
    });

    try {
      await context.read<Session>().api.updateSatellite(
        widget.satellite.id,
        name: _name.text.trim(),
        description: _description.text.trim(),
        isSchedulable: _schedulable,
      );
      if (mounted) Navigator.pop(context, true);
    } catch (error) {
      if (mounted) setState(() => _failure = describeError(error));
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      title: Text('NORAD ${widget.satellite.noradId}'),
      content: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          TextField(
            controller: _name,
            decoration: const InputDecoration(
              labelText: 'Name',
              border: OutlineInputBorder(),
            ),
          ),
          const SizedBox(height: 12),
          TextField(
            controller: _description,
            maxLines: 2,
            decoration: const InputDecoration(
              labelText: 'Description',
              border: OutlineInputBorder(),
            ),
          ),
          SwitchListTile(
            contentPadding: EdgeInsets.zero,
            value: _schedulable,
            onChanged: (value) => setState(() => _schedulable = value),
            title: const Text('Open for scheduling'),
            subtitle: const Text(
              'Turning this off leaves existing passes alone.',
            ),
          ),
          if (_failure != null)
            Text(
              _failure!,
              style: TextStyle(color: Theme.of(context).colorScheme.error),
            ),
        ],
      ),
      actions: [
        TextButton(
          onPressed: _busy ? null : () => Navigator.pop(context),
          child: const Text('Cancel'),
        ),
        FilledButton(
          onPressed: _busy ? null : _submit,
          child: _busy
              ? const SizedBox(
                  height: 16,
                  width: 16,
                  child: CircularProgressIndicator(strokeWidth: 2),
                )
              : const Text('Save'),
        ),
      ],
    );
  }
}
