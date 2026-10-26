import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../../api/api_exception.dart';
import '../../models/models.dart';
import '../../state/session.dart';
import '../../widgets/states.dart';
import '../satellites_screen.dart' show describeError;

class UsersScreen extends StatefulWidget {
  const UsersScreen({super.key});

  @override
  State<UsersScreen> createState() => _UsersScreenState();
}

class _UsersScreenState extends State<UsersScreen> {
  late Future<List<AuthUser>> _future;

  @override
  void initState() {
    super.initState();
    _load();
  }

  void _load() {
    final api = context.read<Session>().api;
    setState(() {
      _future = api.users()..ignore();
    });
  }

  void _report(String message) {
    if (!mounted) return;
    ScaffoldMessenger.of(context)
        .showSnackBar(SnackBar(content: Text(message)));
  }

  Future<void> _create() async {
    final created =
        await showDialog<({String username, String password, String role})>(
          context: context,
          builder: (context) => const _NewUserDialog(),
        );
    if (created == null || !mounted) return;

    try {
      await context.read<Session>().api.createUser(
        username: created.username,
        password: created.password,
        role: created.role,
      );
      _report(
        '${created.username} created. They must change the password '
        'at first sign-in.',
      );
      _load();
    } catch (error) {
      _report(describeError(error));
    }
  }

  Future<void> _changeRole(AuthUser user, String role) async {
    try {
      await context.read<Session>().api.setUserRole(user.id, role);
      _report(
        '${user.username} is now ${role == 'admin' ? 'an admin' : 'a user'}.',
      );
      _load();
    } catch (error) {
      _report(describeError(error));
    }
  }

  Future<void> _delete(AuthUser user) async {
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: Text('Delete ${user.username}?'),
        content: const Text(
          'The audit trail keeps what they did. An account with passes on '
          'record cannot be removed at all.',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context, false),
            child: const Text('Keep'),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(context, true),
            child: const Text('Delete'),
          ),
        ],
      ),
    );
    if (confirmed != true || !mounted) return;

    try {
      await context.read<Session>().api.deleteUser(user.id);
      _report('${user.username} deleted.');
      _load();
    } on ApiException catch (error) {
      _report(
        error.code == 'in_use'
            ? '${user.username} has passes on record, so the account cannot '
                  'be removed. Their history stays either way.'
            : error.message,
      );
    } catch (error) {
      _report(describeError(error));
    }
  }

  @override
  Widget build(BuildContext context) {
    final me = context.watch<Session>().user;

    return Scaffold(
      body: FutureBuilder<List<AuthUser>>(
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

          final users = snapshot.data ?? const <AuthUser>[];
          return ListView.builder(
            padding: const EdgeInsets.fromLTRB(16, 16, 16, 88),
            itemCount: users.length,
            itemBuilder: (context, index) {
              final user = users[index];
              final protected = user.isRoot || user.id == me?.id;

              return Card(
                margin: const EdgeInsets.only(bottom: 8),
                child: ListTile(
                  leading: Icon(
                    user.isRoot
                        ? Icons.shield
                        : user.isAdmin
                        ? Icons.admin_panel_settings_outlined
                        : Icons.person_outline,
                  ),
                  title: Text(user.username),
                  subtitle: Text(
                    user.mustChangePassword
                        ? '${_roleLabel(user.role)} · must change password'
                        : _roleLabel(user.role),
                  ),
                  trailing: protected
                      ? const Text('Protected')
                      : PopupMenuButton<String>(
                          onSelected: (value) => switch (value) {
                            'delete' => _delete(user),
                            _ => _changeRole(user, value),
                          },
                          itemBuilder: (context) => [
                            if (!user.isAdmin)
                              const PopupMenuItem(
                                value: 'admin',
                                child: Text('Promote to admin'),
                              ),
                            if (user.isAdmin)
                              const PopupMenuItem(
                                value: 'user',
                                child: Text('Demote to user'),
                              ),
                            const PopupMenuDivider(),
                            const PopupMenuItem(
                              value: 'delete',
                              child: Text('Delete account'),
                            ),
                          ],
                        ),
                ),
              );
            },
          );
        },
      ),
      floatingActionButton: FloatingActionButton.extended(
        onPressed: _create,
        icon: const Icon(Icons.person_add_alt),
        label: const Text('New account'),
      ),
    );
  }

  static String _roleLabel(String role) => switch (role) {
    'root' => 'Root',
    'admin' => 'Admin',
    _ => 'User',
  };
}

class _NewUserDialog extends StatefulWidget {
  const _NewUserDialog();

  @override
  State<_NewUserDialog> createState() => _NewUserDialogState();
}

class _NewUserDialogState extends State<_NewUserDialog> {
  final _formKey = GlobalKey<FormState>();
  final _username = TextEditingController();
  final _password = TextEditingController();
  String _role = 'user';

  @override
  void dispose() {
    _username.dispose();
    _password.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      title: const Text('New account'),
      content: Form(
        key: _formKey,
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            TextFormField(
              controller: _username,
              decoration: const InputDecoration(
                labelText: 'Username',
                border: OutlineInputBorder(),
              ),
              validator: (value) => (value == null || value.trim().isEmpty)
                  ? 'Enter a username'
                  : null,
            ),
            const SizedBox(height: 12),
            TextFormField(
              controller: _password,
              obscureText: true,
              decoration: const InputDecoration(
                labelText: 'Initial password',
                helperText: 'They must change it at first sign-in.',
                border: OutlineInputBorder(),
              ),
              validator: (value) => (value == null || value.length < 12)
                  ? 'Use at least 12 characters'
                  : null,
            ),
            const SizedBox(height: 16),
            SegmentedButton<String>(
              segments: const [
                ButtonSegment(value: 'user', label: Text('User')),
                ButtonSegment(value: 'admin', label: Text('Admin')),
              ],
              selected: {_role},
              onSelectionChanged: (value) =>
                  setState(() => _role = value.first),
            ),
          ],
        ),
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.pop(context),
          child: const Text('Cancel'),
        ),
        FilledButton(
          onPressed: () {
            if (!_formKey.currentState!.validate()) return;
            Navigator.pop(context, (
              username: _username.text.trim(),
              password: _password.text,
              role: _role,
            ));
          },
          child: const Text('Create'),
        ),
      ],
    );
  }
}
