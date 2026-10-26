import 'package:flutter/material.dart';

class AppDestination {
  const AppDestination({
    required this.label,
    required this.icon,
    required this.selectedIcon,
    this.adminOnly = false,
  });

  final String label;
  final IconData icon;
  final IconData selectedIcon;

  final bool adminOnly;
}

const List<AppDestination> _allDestinations = [
  AppDestination(
    label: 'Dashboard',
    icon: Icons.dashboard_outlined,
    selectedIcon: Icons.dashboard,
  ),
  AppDestination(
    label: 'Satellites',
    icon: Icons.satellite_alt_outlined,
    selectedIcon: Icons.satellite_alt,
  ),
  AppDestination(
    label: 'My passes',
    icon: Icons.event_note_outlined,
    selectedIcon: Icons.event_note,
  ),
  AppDestination(
    label: 'Recordings',
    icon: Icons.folder_outlined,
    selectedIcon: Icons.folder,
  ),
  AppDestination(
    label: 'Public history',
    icon: Icons.public_outlined,
    selectedIcon: Icons.public,
  ),
  AppDestination(
    label: 'Administration',
    icon: Icons.admin_panel_settings_outlined,
    selectedIcon: Icons.admin_panel_settings,
    adminOnly: true,
  ),
];

List<AppDestination> destinationsFor({required bool isAdmin}) => [
  for (final destination in _allDestinations)
    if (isAdmin || !destination.adminOnly) destination,
];
