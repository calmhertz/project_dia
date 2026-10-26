import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../../models/models.dart';
import '../../state/session.dart';
import '../../widgets/formatting.dart';
import '../../widgets/states.dart';
import '../satellites_screen.dart' show describeError;

class AuditScreen extends StatefulWidget {
  const AuditScreen({super.key});

  @override
  State<AuditScreen> createState() => _AuditScreenState();
}

class _AuditScreenState extends State<AuditScreen> {
  late Future<({List<AuditRecord> records, Map<String, String> actors})>
  _future;
  String? _action;

  static const _filters = <String, String>{
    'auth.login_failed': 'Failed sign-ins',
    'user.created': 'Accounts created',
    'user.role_changed': 'Roles changed',
    'pass.cancelled_by_root_override': 'Root overrides',
    'station.updated': 'Station changes',
    'scheduling_config.updated': 'Rule changes',
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
        final records = await api.auditTrail(action: _action);
        Map<String, String> actors = {};
        try {
          actors = {
            for (final user in await api.users()) user.id: user.username,
          };
        } on Object {
          actors = {};
        }
        return (records: records, actors: actors);
      }();
    });
  }

  void _select(String? action) {
    _action = action;
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
                label: const Text('Everything'),
                selected: _action == null,
                onSelected: (_) => _select(null),
              ),
              for (final entry in _filters.entries) ...[
                const SizedBox(width: 8),
                FilterChip(
                  label: Text(entry.value),
                  selected: _action == entry.key,
                  onSelected: (_) => _select(entry.key),
                ),
              ],
            ],
          ),
        ),
        Expanded(
          child:
              FutureBuilder<
                ({List<AuditRecord> records, Map<String, String> actors})
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
                  if (data.records.isEmpty) {
                    return const EmptyState(
                      icon: Icons.fact_check_outlined,
                      title: 'Nothing recorded',
                      message: 'No matching actions have been taken.',
                    );
                  }

                  return RefreshIndicator(
                    onRefresh: () async => _load(),
                    child: ListView.builder(
                      padding: const EdgeInsets.fromLTRB(16, 0, 16, 16),
                      itemCount: data.records.length,
                      itemBuilder: (context, index) {
                        final record = data.records[index];
                        final actor = record.actorUserId == null
                            ? 'the system'
                            : data.actors[record.actorUserId] ??
                                  record.actorUserId!;
                        return Card(
                          margin: const EdgeInsets.only(bottom: 8),
                          child: ListTile(
                            dense: true,
                            leading: Icon(_iconFor(record.action)),
                            title: Text(_describe(record.action)),
                            subtitle: Text(
                              '$actor · ${record.entityType} · '
                              '${formatDateTime(record.createdAt)}',
                            ),
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

  static IconData _iconFor(String action) {
    if (action.startsWith('auth.')) return Icons.login;
    if (action.startsWith('user.')) return Icons.person_outline;
    if (action.startsWith('pass.')) return Icons.event_note_outlined;
    if (action.startsWith('satellite.') || action.startsWith('tle.')) {
      return Icons.satellite_alt_outlined;
    }
    if (action.startsWith('station.') || action.startsWith('system.')) {
      return Icons.settings_input_antenna;
    }
    return Icons.fact_check_outlined;
  }

  static String _describe(String action) => switch (action) {
    'auth.login' => 'Signed in',
    'auth.login_failed' => 'Failed sign-in',
    'auth.password_changed' => 'Password changed',
    'auth.root_password_reset_via_cli' => 'Root password reset from the host',
    'user.created' => 'Account created',
    'user.role_changed' => 'Role changed',
    'user.deleted' => 'Account deleted',
    'system.initialized' => 'System set up',
    'station.updated' => 'Station changed',
    'station.band_updated' => 'Antenna settings changed',
    'station.hardware_updated' => 'Rotator or receiver changed',
    'scheduling_config.updated' => 'Scheduling rules changed',
    'satellite.added' => 'Satellite added',
    'satellite.deleted' => 'Satellite removed',
    'tle.refreshed' => 'Orbital data refreshed',
    'pass.requested' => 'Pass requested',
    'pass.approved' => 'Pass approved',
    'pass.rejected' => 'Pass rejected',
    'pass.cancelled' => 'Pass cancelled',
    'pass.cancelled_by_root_override' => 'Pass cancelled by root override',
    'pass.root_override_created' => 'Pass created by root override',
    _ => action,
  };
}
