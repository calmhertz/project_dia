import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../../models/models.dart';
import '../../state/session.dart';
import '../../widgets/states.dart';
import '../../widgets/pipeline_upload.dart';
import '../satellites_screen.dart' show describeError;

class PipelinesScreen extends StatefulWidget {
  const PipelinesScreen({super.key});

  @override
  State<PipelinesScreen> createState() => _PipelinesScreenState();
}

class _PipelinesScreenState extends State<PipelinesScreen> {
  late Future<PipelineCatalogue> _future;

  @override
  void initState() {
    super.initState();
    _load();
  }

  void _load() {
    final api = context.read<Session>().api;
    setState(() {
      _future = api.pipelines()..ignore();
    });
  }

  Future<void> _upload() async {
    final added = await UploadPipelineDialog.show(context);
    if (added != null && mounted) _load();
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      floatingActionButton: FloatingActionButton.extended(
        onPressed: _upload,
        icon: const Icon(Icons.upload_file),
        label: const Text('Add custom pipeline'),
      ),
      body: FutureBuilder<PipelineCatalogue>(
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

          final catalogue = snapshot.data!;
          if (catalogue.pipelines.isEmpty) {
            return const EmptyState(
              icon: Icons.account_tree_outlined,
              title: 'No pipelines yet',
              message:
                  'The ground station reports what its SatDump installation '
                  'provides when it connects. A custom definition can be '
                  'added here in the meantime.',
            );
          }

          final standard = catalogue.pipelines
              .where((pipeline) => !pipeline.isCustom)
              .toList();
          final custom = catalogue.pipelines
              .where((pipeline) => pipeline.isCustom)
              .toList();

          return RefreshIndicator(
            onRefresh: () async => _load(),
            child: ListView(
              padding: const EdgeInsets.fromLTRB(16, 16, 16, 88),
              children: [
                if (custom.isNotEmpty) ...[
                  _Heading(
                    'Uploaded',
                    'Definitions an operator supplied. These travel with the '
                        'pass, so the station needs nothing from the server '
                        'while it runs.',
                  ),
                  for (final pipeline in custom)
                    _PipelineTile(pipeline: pipeline),
                  const SizedBox(height: 24),
                ],
                _Heading(
                  'From the station',
                  '${standard.length} reported by the ground station\'s '
                      'SatDump installation.',
                ),
                for (final pipeline in standard)
                  _PipelineTile(pipeline: pipeline),
              ],
            ),
          );
        },
      ),
    );
  }
}

class _Heading extends StatelessWidget {
  const _Heading(this.title, this.explanation);

  final String title;
  final String explanation;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Padding(
      padding: const EdgeInsets.only(bottom: 12),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(title, style: theme.textTheme.titleMedium),
          const SizedBox(height: 4),
          Text(
            explanation,
            style: theme.textTheme.bodySmall?.copyWith(
              color: theme.colorScheme.onSurfaceVariant,
            ),
          ),
        ],
      ),
    );
  }
}

class _PipelineTile extends StatelessWidget {
  const _PipelineTile({required this.pipeline});

  final SatDumpPipeline pipeline;

  @override
  Widget build(BuildContext context) {
    return Card(
      margin: const EdgeInsets.only(bottom: 8),
      child: ListTile(
        dense: true,
        leading: Icon(
          pipeline.isCustom ? Icons.description_outlined : Icons.account_tree,
        ),
        title: Text(pipeline.name),
        subtitle: Text(pipeline.isCustom ? 'Uploaded' : 'Standard'),
      ),
    );
  }
}
