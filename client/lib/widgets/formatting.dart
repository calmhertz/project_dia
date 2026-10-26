import 'package:flutter/material.dart';

/// Readable, unambiguous time and status formatting (client-spec section 32).

const _months = [
  'Jan',
  'Feb',
  'Mar',
  'Apr',
  'May',
  'Jun',
  'Jul',
  'Aug',
  'Sep',
  'Oct',
  'Nov',
  'Dec',
];

/// Times are shown in the viewer's local zone, labelled, because an operator
/// reading a schedule should not have to convert in their head.
String formatDateTime(DateTime moment) {
  final local = moment.toLocal();
  return '${local.day} ${_months[local.month - 1]} ${local.year}, '
      '${_two(local.hour)}:${_two(local.minute)}';
}

String formatTime(DateTime moment) {
  final local = moment.toLocal();
  return '${_two(local.hour)}:${_two(local.minute)}:${_two(local.second)}';
}

String formatDate(DateTime moment) {
  final local = moment.toLocal();
  return '${local.day} ${_months[local.month - 1]} ${local.year}';
}

String formatDuration(Duration duration) {
  final minutes = duration.inMinutes;
  final seconds = duration.inSeconds % 60;
  if (minutes == 0) return '${duration.inSeconds}s';
  return '${minutes}m ${seconds}s';
}

String formatRelative(DateTime moment) {
  final difference = moment.difference(DateTime.now().toUtc());
  final future = !difference.isNegative;
  final magnitude = difference.abs();

  String amount;
  if (magnitude.inDays > 0) {
    amount = '${magnitude.inDays}d';
  } else if (magnitude.inHours > 0) {
    amount = '${magnitude.inHours}h';
  } else {
    amount = '${magnitude.inMinutes}m';
  }
  return future ? 'in $amount' : '$amount ago';
}

String formatBytes(int bytes) {
  if (bytes < 1024) return '$bytes B';
  if (bytes < 1024 * 1024) return '${(bytes / 1024).toStringAsFixed(1)} KB';
  if (bytes < 1024 * 1024 * 1024) {
    return '${(bytes / (1024 * 1024)).toStringAsFixed(1)} MB';
  }
  return '${(bytes / (1024 * 1024 * 1024)).toStringAsFixed(2)} GB';
}

/// A pass status shown as text plus an icon, never colour alone.
/// What a pass is set to produce, in words rather than the wire value.
String describeRecordingMode(String mode) => switch (mode) {
  'raw' => 'Raw recording',
  'process' => 'Processed result',
  'raw_and_process' => 'Raw recording and processed result',
  _ => mode,
};

class StatusChip extends StatelessWidget {
  const StatusChip({super.key, required this.status});

  final String status;

  static const _labels = {
    'pending_approval': 'Pending approval',
    'approved': 'Approved',
    'rejected': 'Rejected',
    'cancelled': 'Cancelled',
    'executing': 'Executing',
    'completed': 'Completed',
    'failed': 'Failed',
    'missed': 'Missed',
  };

  static const _icons = {
    'pending_approval': Icons.hourglass_empty,
    'approved': Icons.event_available,
    'rejected': Icons.block,
    'cancelled': Icons.cancel_outlined,
    'executing': Icons.settings_input_antenna,
    'completed': Icons.check_circle_outline,
    'failed': Icons.error_outline,
    'missed': Icons.timer_off_outlined,
  };

  @override
  Widget build(BuildContext context) {
    final colours = Theme.of(context).colorScheme;

    Color background;
    Color foreground;
    switch (status) {
      case 'completed':
      case 'approved':
        background = colours.primaryContainer;
        foreground = colours.onPrimaryContainer;
      case 'failed':
      case 'rejected':
      case 'missed':
        background = colours.errorContainer;
        foreground = colours.onErrorContainer;
      case 'executing':
        background = colours.tertiaryContainer;
        foreground = colours.onTertiaryContainer;
      default:
        background = colours.surfaceContainerHighest;
        foreground = colours.onSurfaceVariant;
    }

    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 5),
      decoration: BoxDecoration(
        color: background,
        borderRadius: BorderRadius.circular(999),
      ),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          Icon(
            _icons[status] ?? Icons.help_outline,
            size: 14,
            color: foreground,
          ),
          const SizedBox(width: 6),
          Text(
            _labels[status] ?? status,
            style: Theme.of(context).textTheme.labelMedium
                ?.copyWith(color: foreground),
          ),
        ],
      ),
    );
  }
}

String _two(int value) => value.toString().padLeft(2, '0');
