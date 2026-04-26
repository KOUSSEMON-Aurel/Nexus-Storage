import 'package:flutter/material.dart';
import 'package:flutter_foreground_task/flutter_foreground_task.dart';

/// A widget that minimize the app without closing it when the user presses the soft back button.
/// It only works when the service is running.
///
/// This widget must be declared above the [Scaffold] widget.
class WithForegroundTask extends StatefulWidget {
  /// A child widget that contains the [Scaffold] widget.
  final Widget child;

  const WithForegroundTask({super.key, required this.child});

  @override
  State<StatefulWidget> createState() => _WithForegroundTaskState();
}

class _WithForegroundTaskState extends State<WithForegroundTask> {
  @override
  Widget build(BuildContext context) => PopScope(
        canPop: false,
        onPopInvokedWithResult: (bool didPop, dynamic result) async {
          if (didPop) return;

          final bool canPop = mounted ? Navigator.canPop(context) : false;
          if (!canPop && await FlutterForegroundTask.isRunningService) {
            FlutterForegroundTask.minimizeApp();
          } else {
            if (context.mounted) {
              Navigator.of(context).pop();
            }
          }
        },
        child: widget.child,
      );
}
