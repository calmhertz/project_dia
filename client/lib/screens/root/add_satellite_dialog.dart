import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../../api/api_exception.dart';
import '../../state/session.dart';

/// Add a satellite by catalog number, Root only.
///
/// The catalog number is the identity; the name is only a label. The Server
/// fetches orbital data before it stores anything, so a satellite that
/// appears in the catalogue can always be tracked (spec.md section 11.1).
class AddSatelliteDialog extends StatefulWidget {
  const AddSatelliteDialog({super.key});

  @override
  State<AddSatelliteDialog> createState() => _AddSatelliteDialogState();
}

class _AddSatelliteDialogState extends State<AddSatelliteDialog> {
  final _formKey = GlobalKey<FormState>();
  final _noradId = TextEditingController();
  final _name = TextEditingController();
  bool _busy = false;
  String? _failure;

  @override
  void dispose() {
    _noradId.dispose();
    _name.dispose();
    super.dispose();
  }

  Future<void> _submit() async {
    if (!_formKey.currentState!.validate()) return;
    setState(() {
      _busy = true;
      _failure = null;
    });

    try {
      final satellite = await context.read<Session>().api.addSatellite(
        noradId: int.parse(_noradId.text.trim()),
        name: _name.text.trim(),
      );
      if (!mounted) return;
      Navigator.pop(context, true);
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('${satellite.name} added to the catalogue.')),
      );
    } on ApiException catch (error) {
      if (mounted) setState(() => _failure = _explain(error));
    } on NetworkException catch (error) {
      if (mounted) setState(() => _failure = error.message);
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  /// The two refusals worth their own wording: everything else is the
  /// Server's message, which is already written for a reader.
  static String _explain(ApiException error) => switch (error.code) {
    'no_orbital_data' =>
      'No orbital data provider has this catalog number. Check the number, '
          'or try again later if a provider is down.',
    'conflict' => 'That satellite is already in the catalogue.',
    _ => error.message,
  };

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      title: const Text('Add satellite'),
      content: Form(
        key: _formKey,
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            TextFormField(
              controller: _noradId,
              autofocus: true,
              keyboardType: TextInputType.number,
              decoration: const InputDecoration(
                labelText: 'NORAD catalog number',
                helperText: 'For example 25544 for the ISS.',
                border: OutlineInputBorder(),
              ),
              validator: (value) {
                final parsed = int.tryParse((value ?? '').trim());
                if (parsed == null) return 'Enter a catalog number';
                if (parsed <= 0) return 'Catalog numbers are positive';
                return null;
              },
            ),
            const SizedBox(height: 12),
            TextFormField(
              controller: _name,
              decoration: const InputDecoration(
                labelText: 'Name (optional)',
                helperText: 'Left empty, the provider name is used.',
                border: OutlineInputBorder(),
              ),
            ),
            if (_failure != null) ...[
              const SizedBox(height: 16),
              Text(
                _failure!,
                style: TextStyle(color: Theme.of(context).colorScheme.error),
              ),
            ],
          ],
        ),
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
              : const Text('Add'),
        ),
      ],
    );
  }
}
