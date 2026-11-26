import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../models/models.dart';
import '../state/notification_center.dart';
import '../widgets/formatting.dart';
import '../widgets/states.dart';
import 'pass_detail_screen.dart';

IconData notificationIcon(String severity) => switch (severity) {
  'success' => Icons.check_circle_outline,
  'warning' => Icons.warning_amber_outlined,
  'error' => Icons.error_outline,
  _ => Icons.notifications_none,
};

Color notificationColor(BuildContext context, String severity) {
  final scheme = Theme.of(context).colorScheme;
  return switch (severity) {
    'success' => Colors.green.shade400,
    'warning' => Colors.amber.shade400,
    'error' => scheme.error,
    _ => scheme.primary,
  };
}

/// Opens what a notification is about and marks it read.
void openNotification(BuildContext context, AppNotification notification) {
  context.read<NotificationCenter>().markRead(notification);
  final passId = notification.passId;
  if (passId == null) return;
  Navigator.of(
    context,
  ).push(MaterialPageRoute(builder: (_) => PassDetailScreen(passId: passId)));
}

class NotificationsScreen extends StatelessWidget {
  const NotificationsScreen({super.key});

  @override
  Widget build(BuildContext context) {
    final center = context.watch<NotificationCenter>();
    final items = center.items;

    return Scaffold(
      appBar: AppBar(
        title: const Text('Notifications'),
        actions: [
          if (center.unreadCount > 0)
            TextButton(
              onPressed: center.markAllRead,
              child: const Text('Mark all read'),
            ),
        ],
      ),
      body: !center.loaded
          ? const LoadingState(message: 'Loading notifications')
          : items.isEmpty
          ? const EmptyState(
              icon: Icons.notifications_none,
              title: 'No notifications',
              message:
                  'Pass approvals, recordings and station alerts show up here.',
            )
          : RefreshIndicator(
              onRefresh: center.refresh,
              child: ListView.separated(
                itemCount: items.length,
                separatorBuilder: (_, _) => const Divider(height: 1),
                itemBuilder: (context, index) {
                  final n = items[index];
                  final theme = Theme.of(context);
                  return ListTile(
                    leading: Icon(
                      notificationIcon(n.severity),
                      color: notificationColor(context, n.severity),
                    ),
                    title: Text(
                      n.title,
                      style: n.isRead
                          ? null
                          : const TextStyle(fontWeight: FontWeight.w600),
                    ),
                    subtitle: Text(n.body),
                    trailing: Column(
                      mainAxisAlignment: MainAxisAlignment.center,
                      crossAxisAlignment: CrossAxisAlignment.end,
                      children: [
                        Text(
                          formatRelative(n.createdAt),
                          style: theme.textTheme.bodySmall,
                        ),
                        if (!n.isRead) ...[
                          const SizedBox(height: 6),
                          Container(
                            width: 8,
                            height: 8,
                            decoration: BoxDecoration(
                              color: theme.colorScheme.primary,
                              shape: BoxShape.circle,
                            ),
                          ),
                        ],
                      ],
                    ),
                    onTap: () => openNotification(context, n),
                  );
                },
              ),
            ),
    );
  }
}

/// The app bar bell with the unread count.
class NotificationBell extends StatelessWidget {
  const NotificationBell({super.key});

  @override
  Widget build(BuildContext context) {
    final unread = context.select<NotificationCenter, int>(
      (c) => c.unreadCount,
    );
    return IconButton(
      tooltip: 'Notifications',
      icon: Badge(
        isLabelVisible: unread > 0,
        label: Text(unread > 99 ? '99+' : '$unread'),
        child: Icon(
          unread > 0 ? Icons.notifications : Icons.notifications_none,
        ),
      ),
      onPressed: () {
        final center = context.read<NotificationCenter>();
        Navigator.of(context).push(
          MaterialPageRoute(
            builder: (_) => ChangeNotifierProvider.value(
              value: center,
              child: const NotificationsScreen(),
            ),
          ),
        );
      },
    );
  }
}
