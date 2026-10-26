import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../models/models.dart';
import '../screens/root/setup_screen.dart';
import '../state/session.dart';
import '../widgets/states.dart';
import 'app_shell.dart';

/// Sends Root to first-run setup, and everyone else to the app.
///
/// Before setup the Server has no station, so predictions and scheduling are
/// refused. Rather than letting a user meet that as a wall of errors, the app
/// says who has to act (spec.md section 17.1).
class FirstRunGate extends StatefulWidget {
  const FirstRunGate({super.key});

  @override
  State<FirstRunGate> createState() => _FirstRunGateState();
}

class _FirstRunGateState extends State<FirstRunGate> {
  late Future<SetupStatus> _future;

  @override
  void initState() {
    super.initState();
    _load();
  }

  void _load() {
    final api = context.read<Session>().api;
    setState(() {
      _future = api.setupStatus()..ignore();
    });
  }

  @override
  Widget build(BuildContext context) {
    final user = context.watch<Session>().user;

    return FutureBuilder<SetupStatus>(
      future: _future,
      builder: (context, snapshot) {
        if (snapshot.connectionState == ConnectionState.waiting) {
          return const Scaffold(body: LoadingState());
        }
        // If the check itself fails, carry on into the app rather than
        // blocking on a question that is not the user's to answer.
        if (snapshot.hasError || (snapshot.data?.initialized ?? true)) {
          return const AppShell();
        }

        if (user?.isRoot ?? false) {
          return SetupScreen(onCompleted: _load);
        }
        return Scaffold(
          body: EmptyState(
            icon: Icons.settings_suggest_outlined,
            title: 'This station is not set up yet',
            message:
                'The root account has to complete first-run setup before '
                'passes can be scheduled.',
            action: FilledButton(
              onPressed: _load,
              child: const Text('Check again'),
            ),
          ),
        );
      },
    );
  }
}
