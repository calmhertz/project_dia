import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../screens/admin/admin_screen.dart';
import '../screens/dashboard_screen.dart';
import '../screens/passes_screen.dart';
import '../screens/recordings_screen.dart';
import '../screens/satellites_screen.dart';
import '../state/session.dart';
import 'destinations.dart';

const double _compactWidthLimit = 600;

class AppShell extends StatefulWidget {
  const AppShell({super.key});

  @override
  State<AppShell> createState() => _AppShellState();
}

class _AppShellState extends State<AppShell> {
  int _selectedIndex = 0;

  void _select(int index) => setState(() => _selectedIndex = index);

  Widget _screenFor(String label) => switch (label) {
    'Dashboard' => const DashboardScreen(),
    'Satellites' => const SatellitesScreen(),
    'My passes' => const PassesScreen(scope: 'mine'),
    'Recordings' => const RecordingsScreen(),
    'Public history' => const PassesScreen(scope: 'public'),
    _ => const AdminScreen(),
  };

  @override
  Widget build(BuildContext context) {
    final isCompact = MediaQuery.sizeOf(context).width < _compactWidthLimit;
    final session = context.watch<Session>();
    final destinations = destinationsFor(
      isAdmin: session.user?.isAdmin ?? false,
    );
    final selected = _selectedIndex.clamp(0, destinations.length - 1);
    final destination = destinations[selected];

    final body = KeyedSubtree(
      key: ValueKey(destination.label),
      child: _screenFor(destination.label),
    );

    return Scaffold(
      appBar: AppBar(
        title: Text(destination.label),
        actions: [
          PopupMenuButton<String>(
            icon: const Icon(Icons.account_circle_outlined),
            tooltip: session.user?.username ?? 'Account',
            onSelected: (value) {
              if (value == 'sign-out') session.signOut();
            },
            itemBuilder: (context) => [
              PopupMenuItem<String>(
                enabled: false,
                child: ListTile(
                  contentPadding: EdgeInsets.zero,
                  title: Text(session.user?.username ?? ''),
                  subtitle: Text(session.user?.role ?? ''),
                ),
              ),
              const PopupMenuDivider(),
              const PopupMenuItem<String>(
                value: 'sign-out',
                child: ListTile(
                  contentPadding: EdgeInsets.zero,
                  leading: Icon(Icons.logout),
                  title: Text('Sign out'),
                ),
              ),
            ],
          ),
        ],
      ),
      body: isCompact
          ? body
          : Row(
              children: [
                NavigationRail(
                  selectedIndex: selected,
                  onDestinationSelected: _select,
                  labelType: NavigationRailLabelType.all,
                  destinations: [
                    for (final item in destinations)
                      NavigationRailDestination(
                        icon: Icon(item.icon),
                        selectedIcon: Icon(item.selectedIcon),
                        label: Text(item.label),
                      ),
                  ],
                ),
                const VerticalDivider(width: 1),
                Expanded(child: body),
              ],
            ),
      bottomNavigationBar: isCompact
          ? NavigationBar(
              selectedIndex: selected,
              onDestinationSelected: _select,
              destinations: [
                for (final item in destinations)
                  NavigationDestination(
                    icon: Icon(item.icon),
                    selectedIcon: Icon(item.selectedIcon),
                    label: item.label,
                  ),
              ],
            )
          : null,
    );
  }
}
