import 'dart:convert';

import 'package:file_selector/file_selector.dart';
import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../models/models.dart';
import '../screens/satellites_screen.dart' show describeError;
import '../state/session.dart';

/// Upload a custom SatDump pipeline (spec.md section 19.1, client-spec
/// section 12).
///
/// A file is chosen or the JSON is pasted; the client checks only the obvious
/// things - that it is a file of plausible size and parses as JSON - and lets
/// the Server be the authority on whether the definition is usable. There is
/// no pipeline editor, deliberately.
class UploadPipelineDialog extends StatefulWidget {
  const UploadPipelineDialog({super.key});

  /// Returns the pipeline the Server accepted, or null if cancelled.
  static Future<SatDumpPipeline?> show(BuildContext context) {
    return showDialog<SatDumpPipeline>(
      context: context,
      builder: (context) => const UploadPipelineDialog(),
    );
  }

  @override
  State<UploadPipelineDialog> createState() => _UploadPipelineDialogState();
}

class _UploadPipelineDialogState extends State<UploadPipelineDialog> {
  /// A pipeline definition is a small text file; anything larger is a
  /// mistake, and the Server refuses it too.
  static const int _maxBytes = 256 * 1024;

  final _name = TextEditingController();
  final _definition = TextEditingController();
  String? _fileName;
  bool _busy = false;
  String? _failure;

  @override
  void dispose() {
    _name.dispose();
    _definition.dispose();
    super.dispose();
  }

  Future<void> _choose() async {
    const type = XTypeGroup(
      label: 'SatDump pipeline',
      extensions: ['json'],
      mimeTypes: ['application/json'],
    );

    final file = await openFile(acceptedTypeGroups: const [type]);
    if (file == null) return;

    final bytes = await file.readAsBytes();
    if (bytes.length > _maxBytes) {
      setState(() {
        _failure =
            'That file is larger than 256 KB, which is not a pipeline '
            'definition.';
      });
      return;
    }

    String text;
    try {
      text = utf8.decode(bytes);
    } on FormatException {
      setState(() => _failure = 'That file is not text.');
      return;
    }

    setState(() {
      _fileName = file.name;
      _definition.text = text;
      _failure = null;
      if (_name.text.trim().isEmpty) {
        // A sensible default the operator can overwrite.
        _name.text = file.name.replaceAll(RegExp(r'\.json$'), '');
      }
    });
  }

  Future<void> _submit() async {
    if (_name.text.trim().isEmpty) {
      setState(() => _failure = 'Give the pipeline a name.');
      return;
    }
    if (_definition.text.trim().isEmpty) {
      setState(() => _failure = 'Choose a file or paste the pipeline JSON.');
      return;
    }
    // A local parse catches the common mistake immediately; the Server still
    // decides whether the structure is acceptable.
    try {
      jsonDecode(_definition.text);
    } on FormatException catch (error) {
      setState(() => _failure = 'That is not valid JSON: ${error.message}');
      return;
    }

    setState(() {
      _busy = true;
      _failure = null;
    });
    try {
      final pipeline = await context.read<Session>().api.uploadPipeline(
        name: _name.text.trim(),
        definition: _definition.text,
      );
      if (mounted) Navigator.pop(context, pipeline);
    } catch (error) {
      if (mounted) setState(() => _failure = describeError(error));
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);

    return AlertDialog(
      title: const Text('Upload pipeline JSON'),
      content: SizedBox(
        width: 520,
        child: SingleChildScrollView(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Row(
                children: [
                  OutlinedButton.icon(
                    onPressed: _busy ? null : _choose,
                    icon: const Icon(Icons.folder_open),
                    label: const Text('Choose file'),
                  ),
                  const SizedBox(width: 12),
                  Expanded(
                    child: Text(
                      _fileName ?? 'No file chosen',
                      style: theme.textTheme.bodySmall?.copyWith(
                        color: theme.colorScheme.onSurfaceVariant,
                      ),
                      overflow: TextOverflow.ellipsis,
                    ),
                  ),
                ],
              ),
              const SizedBox(height: 16),
              TextField(
                controller: _name,
                decoration: const InputDecoration(
                  labelText: 'Name',
                  border: OutlineInputBorder(),
                ),
              ),
              const SizedBox(height: 12),
              TextField(
                controller: _definition,
                maxLines: 8,
                minLines: 4,
                style: const TextStyle(fontFamily: 'monospace', fontSize: 12),
                decoration: const InputDecoration(
                  labelText: 'Pipeline JSON',
                  helperText: 'From the chosen file, or pasted directly.',
                  border: OutlineInputBorder(),
                  alignLabelWithHint: true,
                ),
              ),
              if (_failure != null) ...[
                const SizedBox(height: 12),
                Text(
                  _failure!,
                  style: TextStyle(color: theme.colorScheme.error),
                ),
              ],
            ],
          ),
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
              : const Text('Upload'),
        ),
      ],
    );
  }
}
