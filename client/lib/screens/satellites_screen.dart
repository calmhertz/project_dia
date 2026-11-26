import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../api/api_exception.dart';
import '../models/models.dart';
import '../state/session.dart';
import '../widgets/states.dart';
import 'root/add_satellite_dialog.dart';
import 'satellite_detail_screen.dart';

class SatellitesScreen extends StatefulWidget {
  const SatellitesScreen({super.key});

  @override
  State<SatellitesScreen> createState() => _SatellitesScreenState();
}

class _SatellitesScreenState extends State<SatellitesScreen> {
  late Future<List<Satellite>> _future;
  String _query = '';

  @override
  void initState() {
    super.initState();
    _future = context.read<Session>().api.satellites()..ignore();
  }

  void _reload() {
    final api = context.read<Session>().api;
    setState(() {
      _future = api.satellites()..ignore();
    });
  }

  Future<void> _add() async {
    final added = await showDialog<bool>(
      context: context,
      builder: (context) => const AddSatelliteDialog(),
    );
    if (added == true && mounted) _reload();
  }

  @override
  Widget build(BuildContext context) {
    final isRoot = context.watch<Session>().user?.isRoot ?? false;

    return Scaffold(
      floatingActionButton: isRoot
          ? FloatingActionButton.extended(
              onPressed: _add,
              icon: const Icon(Icons.add),
              label: const Text('Add satellite'),
            )
          : null,
      body: _list(isRoot),
    );
  }

  Widget _list(bool isRoot) {
    return FutureBuilder<List<Satellite>>(
      future: _future,
      builder: (context, snapshot) {
        if (snapshot.connectionState == ConnectionState.waiting) {
          return const LoadingState(message: 'Loading the catalogue');
        }
        if (snapshot.hasError) {
          return ErrorState(
            message: describeError(snapshot.error),
            onRetry: _reload,
          );
        }

        final all = snapshot.data ?? const <Satellite>[];
        if (all.isEmpty) {
          return EmptyState(
            icon: Icons.satellite_alt_outlined,
            title: 'No satellites yet',
            message: isRoot
                ? 'Add one by its NORAD catalog number to start scheduling.'
                : 'An administrator adds satellites to the catalogue '
                      'before passes can be scheduled.',
            action: isRoot
                ? FilledButton.icon(
                    onPressed: _add,
                    icon: const Icon(Icons.add),
                    label: const Text('Add satellite'),
                  )
                : null,
          );
        }

        final needle = _query.trim().toLowerCase();
        final visible = needle.isEmpty
            ? all
            : all
                  .where(
                    (satellite) =>
                        satellite.name.toLowerCase().contains(needle) ||
                        satellite.noradId.toString().contains(needle),
                  )
                  .toList();

        return Column(
          children: [
            Padding(
              padding: const EdgeInsets.fromLTRB(16, 12, 16, 8),
              child: SearchBar(
                hintText: 'Search by name or NORAD ID',
                leading: const Icon(Icons.search),
                onChanged: (value) => setState(() => _query = value),
              ),
            ),
            if (visible.isEmpty)
              const Expanded(
                child: EmptyState(
                  icon: Icons.search_off,
                  title: 'No matches',
                  message: 'No satellite matches that search.',
                ),
              )
            else
              Expanded(
                child: RefreshIndicator(
                  onRefresh: () async => _reload(),
                  child: ListView.builder(
                    padding: const EdgeInsets.only(bottom: 88),
                    itemCount: visible.length,
                    itemBuilder: (context, index) =>
                        _SatelliteTile(satellite: visible[index]),
                  ),
                ),
              ),
          ],
        );
      },
    );
  }
}

class _SatelliteTile extends StatelessWidget {
  const _SatelliteTile({required this.satellite});

  final Satellite satellite;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final subtitle = <String>[
      'NORAD ${satellite.noradId}',
      if (satellite.status != null) satellite.status!,
      if (satellite.countries != null) satellite.countries!,
    ].join(' · ');

    return ListTile(
      leading: CircleAvatar(
        backgroundColor: theme.colorScheme.surfaceContainerHighest,
        child: Icon(
          Icons.satellite_alt,
          color: theme.colorScheme.onSurfaceVariant,
        ),
      ),
      title: Text(satellite.name),
      subtitle: Text(subtitle),
      trailing: satellite.isSchedulable
          ? const Icon(Icons.chevron_right)
          : Tooltip(
              message: 'Not available for scheduling',
              child: Icon(
                Icons.do_not_disturb_on_outlined,
                color: theme.colorScheme.onSurfaceVariant,
              ),
            ),
      onTap: () => Navigator.of(context).push(
        MaterialPageRoute(
          builder: (_) => SatelliteDetailScreen(satelliteId: satellite.id),
        ),
      ),
    );
  }
}

String describeError(Object? error) {
  if (error is ApiException) return error.message;
  if (error is NetworkException) return error.message;
  return 'Something went wrong. Please try again.';
}
