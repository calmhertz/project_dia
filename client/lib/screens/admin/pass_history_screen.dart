import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../../models/models.dart';
import '../../state/session.dart';
import '../../widgets/formatting.dart';
import '../../widgets/states.dart';
import '../pass_detail_screen.dart';
import '../satellites_screen.dart' show describeError;

/// Every pass on the station, whoever requested it.
///
/// This is the operational record: cancelled and rejected passes stay
/// visible, because history that quietly drops its failures is not history
/// (spec.md section 13).
class PassHistoryScreen extends StatefulWidget {
  const PassHistoryScreen({super.key});

  @override
  State<PassHistoryScreen> createState() => _PassHistoryScreenState();
}

class _PassHistoryScreenState extends State<PassHistoryScreen> {
  late Future<({List<Pass> passes, Map<String, Satellite> satellites})> _future;
  String? _status;

  static const _filters = <String, String>{
    'pending_approval': 'Pending',
    'approved': 'Approved',
    'executing': 'Executing',
    'completed': 'Completed',
    'failed': 'Failed',
    'missed': 'Missed',
    'cancelled': 'Cancelled',
    'rejected': 'Rejected',
  };

  @override
  void initState() {
    super.initState();
    _load();
  }

  void _load() {
    final api = context.read<Session>().api;
    setState(() {
      _future = () async {
        final passes = await api.passes(scope: 'all', status: _status);
        final catalogue = await api.satellites();
        return (
          passes: passes..sort((a, b) => b.aos.compareTo(a.aos)),
          satellites: {for (final entry in catalogue) entry.id: entry},
        );
      }();
    });
  }

  void _select(String? status) {
    _status = status;
    _load();
  }

  @override
  Widget build(BuildContext context) {
    return Column(
      children: [
        SingleChildScrollView(
          scrollDirection: Axis.horizontal,
          padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
          child: Row(
            children: [
              FilterChip(
                label: const Text('All'),
                selected: _status == null,
                onSelected: (_) => _select(null),
              ),
              for (final entry in _filters.entries) ...[
                const SizedBox(width: 8),
                FilterChip(
                  label: Text(entry.value),
                  selected: _status == entry.key,
                  onSelected: (_) => _select(entry.key),
                ),
              ],
            ],
          ),
        ),
        Expanded(
          child:
              FutureBuilder<
                ({List<Pass> passes, Map<String, Satellite> satellites})
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

                  final data = snapshot.data!;
                  if (data.passes.isEmpty) {
                    return EmptyState(
                      icon: Icons.history,
                      title: 'Nothing to show',
                      message: _status == null
                          ? 'No passes have been requested yet.'
                          : 'No passes are ${_filters[_status]!.toLowerCase()}.',
                    );
                  }

                  return RefreshIndicator(
                    onRefresh: () async => _load(),
                    child: ListView.builder(
                      padding: const EdgeInsets.fromLTRB(16, 0, 16, 16),
                      itemCount: data.passes.length,
                      itemBuilder: (context, index) {
                        final pass = data.passes[index];
                        final satellite = data.satellites[pass.satelliteId];
                        return Card(
                          margin: const EdgeInsets.only(bottom: 8),
                          child: ListTile(
                            title: Text(satellite?.name ?? 'Satellite'),
                            subtitle: Text(
                              '${formatDateTime(pass.aos)} · '
                              '${pass.band.toUpperCase()} · '
                              '${pass.isPublic ? 'Public' : 'Private'}',
                            ),
                            trailing: StatusChip(status: pass.status),
                            onTap: () async {
                              await Navigator.of(context).push(
                                MaterialPageRoute(
                                  builder: (_) => PassDetailScreen(
                                    passId: pass.id,
                                    satelliteName: satellite?.name,
                                  ),
                                ),
                              );
                              _load();
                            },
                          ),
                        );
                      },
                    ),
                  );
                },
              ),
        ),
      ],
    );
  }
}
