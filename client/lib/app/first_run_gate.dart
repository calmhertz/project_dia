import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../models/models.dart';
import '../screens/root/setup_screen.dart';
import '../state/session.dart';
import '../widgets/states.dart';
import 'app_shell.dart';

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
