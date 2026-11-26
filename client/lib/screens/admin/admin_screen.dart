import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../../state/session.dart';
import '../root/audit_screen.dart';
import '../root/configuration_screen.dart';
import '../root/users_screen.dart';
import 'approvals_screen.dart';
import 'operations_screen.dart';
import 'orbital_data_screen.dart';
import 'pipelines_screen.dart';
import 'pass_history_screen.dart';

class AdminScreen extends StatelessWidget {
  const AdminScreen({super.key});

  static const _adminTabs = [
    (Tab(text: 'Approvals', icon: Icon(Icons.rule)), ApprovalsScreen()),
    (
      Tab(text: 'Station', icon: Icon(Icons.settings_input_antenna)),
      OperationsScreen(),
    ),
    (
      Tab(text: 'Orbital data', icon: Icon(Icons.satellite_alt_outlined)),
      OrbitalDataScreen(),
    ),
    (
      Tab(text: 'Pipelines', icon: Icon(Icons.account_tree_outlined)),
      PipelinesScreen(),
    ),
    (Tab(text: 'History', icon: Icon(Icons.history)), PassHistoryScreen()),
  ];

  static const _rootTabs = [
    (Tab(text: 'Configuration', icon: Icon(Icons.tune)), ConfigurationScreen()),
    (Tab(text: 'Accounts', icon: Icon(Icons.group_outlined)), UsersScreen()),
    (Tab(text: 'Audit', icon: Icon(Icons.fact_check_outlined)), AuditScreen()),
  ];

  @override
  Widget build(BuildContext context) {
    final isRoot = context.watch<Session>().user?.isRoot ?? false;
    final tabs = [..._adminTabs, if (isRoot) ..._rootTabs];

    return DefaultTabController(
      length: tabs.length,
      child: Column(
        children: [
          TabBar(tabs: [for (final tab in tabs) tab.$1], isScrollable: true),
          Expanded(
            child: TabBarView(children: [for (final tab in tabs) tab.$2]),
          ),
        ],
      ),
    );
  }
}
