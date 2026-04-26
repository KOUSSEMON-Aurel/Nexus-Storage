import 'package:flutter/material.dart';
import 'dart:ui';
import 'dart:math' as math;

class SpeedDialFab extends StatefulWidget {
  final Map<String, IconData> actions;
  final Map<String, Map<String, IconData>>? nestedActions;
  final Function(String) onActionTap;

  const SpeedDialFab({
    super.key,
    required this.actions,
    this.nestedActions,
    required this.onActionTap,
  });

  @override
  State<SpeedDialFab> createState() => _SpeedDialFabState();
}

class _SpeedDialFabState extends State<SpeedDialFab>
    with SingleTickerProviderStateMixin {
  late AnimationController _controller;
  bool _isOpen = false;
  String? _activeMenu; // null for root, or the key for nestedActions

  @override
  void initState() {
    super.initState();
    _controller = AnimationController(
      duration: const Duration(milliseconds: 300),
      vsync: this,
    );
  }

  @override
  void dispose() {
    _controller.dispose();
    super.dispose();
  }

  void _toggle() {
    if (_activeMenu != null) {
      // If in nested menu, back button returns to root
      setState(() {
        _activeMenu = null;
      });
      return;
    }

    setState(() {
      _isOpen = !_isOpen;
      _isOpen ? _controller.forward() : _controller.reverse();
    });
  }

  void _handleAction(String actionName) {
    if (widget.nestedActions != null &&
        widget.nestedActions!.containsKey(actionName)) {
      // Enter nested menu
      setState(() {
        _activeMenu = actionName;
      });
    } else {
      // Trigger final action
      if (_isOpen) _toggle(); // Close if it was open
      widget.onActionTap(actionName);
    }
  }

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;

    final mainFabBg = isDark
        ? const Color(0xFF6366F1)
        : const Color(0xFF1A73E8);
    final miniFabBg = isDark ? const Color(0xFF1E293B) : Colors.white;
    final miniFabIconColor = isDark ? Colors.white : const Color(0xFF1A73E8);

    return Stack(
      children: [
        // ── Backdrop overlay ──────────────
        if (_isOpen)
          Positioned.fill(
            child: GestureDetector(
              onTap: () {
                setState(() {
                  _isOpen = false;
                  _activeMenu = null;
                  _controller.reverse();
                });
              },
              behavior: HitTestBehavior.opaque,
              child: BackdropFilter(
                filter: ImageFilter.blur(
                  sigmaX: 4,
                  sigmaY: 4,
                ), // Reduced blur from 10
                child: Container(
                  color: isDark
                      ? Colors.black.withValues(alpha: 0.40)
                      : Colors.white.withValues(alpha: 0.30),
                ),
              ),
            ),
          ),

        // ── FAB cluster ───────────────────────────────────────────────────
        Positioned(
          right: 24,
          bottom: 24 + MediaQuery.of(context).padding.bottom,
          child: SizedBox(
            width: 220,
            height: 220,
            child: Stack(
              alignment: Alignment.bottomRight,
              clipBehavior: Clip.none,
              children: [
                // ── Child action buttons ─────────────────────────────────
                ..._buildActionButtons(isDark, miniFabBg, miniFabIconColor),

                // ── Main FAB ─────────────────────────────────────────────
                _AnimatedFab(
                  controller: _controller,
                  color: mainFabBg,
                  onTap: _toggle,
                  isNestedOpen: _activeMenu != null,
                ),
              ],
            ),
          ),
        ),
      ],
    );
  }

  List<Widget> _buildActionButtons(
    bool isDark,
    Color bgColor,
    Color iconColor,
  ) {
    final Map<String, IconData> currentActions = _activeMenu != null
        ? (widget.nestedActions![_activeMenu!] ?? {})
        : widget.actions;

    final int count = currentActions.length;
    const double radius = 115.0;
    final List<Widget> result = [];

    int i = 0;
    for (final entry in currentActions.entries) {
      final double angle = count == 1
          ? math.pi / 4
          : (i * (math.pi / 2)) / (count - 1);

      final double dxUnit = math.cos(angle);
      final double dyUnit = math.sin(angle);

      final int capturedIndex = i;
      final String actionName = entry.key;
      final IconData actionIcon = entry.value;

      result.add(
        AnimatedBuilder(
          animation: _controller,
          key: ValueKey(
            '$_activeMenu-$actionName',
          ), // Force rebuild for nested transitions
          builder: (context, child) {
            double t = 0.0;
            if (_controller.value > 0.1) {
              final double start = capturedIndex * (0.25 / count);
              final double end = (start + 0.75).clamp(0.0, 1.0);
              if (_controller.value >= end) {
                t = 1.0;
              } else if (_controller.value > start) {
                t = (_controller.value - start) / (end - start);
                t = Curves.easeOutBack.transform(t).clamp(0.0, 2.0);
              }
            }

            return Positioned(
              right: dxUnit * radius * t,
              bottom: dyUnit * radius * t,
              child: IgnorePointer(
                ignoring: t < 0.05,
                child: Opacity(
                  opacity: (t * 2).clamp(0.0, 1.0),
                  child: Transform.scale(
                    scale: t.clamp(0.0, 1.2),
                    alignment: Alignment.bottomRight,
                    child: child,
                  ),
                ),
              ),
            );
          },
          child: _MiniActionButton(
            icon: actionIcon,
            label: actionName,
            bgColor: bgColor,
            iconColor: iconColor,
            isDark: isDark,
            onTap: () => _handleAction(actionName),
            heroTag: "speed_dial_${_activeMenu ?? 'root'}_$actionName",
          ),
        ),
      );
      i++;
    }
    return result;
  }
}

class _AnimatedFab extends StatelessWidget {
  final AnimationController controller;
  final Color color;
  final VoidCallback onTap;
  final bool isNestedOpen;

  const _AnimatedFab({
    required this.controller,
    required this.color,
    required this.onTap,
    required this.isNestedOpen,
  });

  @override
  Widget build(BuildContext context) {
    return FloatingActionButton(
      heroTag: "nexus_main_fab",
      backgroundColor: color,
      elevation: 6,
      onPressed: onTap,
      child: AnimatedBuilder(
        animation: controller,
        builder: (context, child) {
          if (isNestedOpen) {
            return const Icon(Icons.arrow_back, color: Colors.white, size: 28);
          }
          return Transform.rotate(
            angle:
                controller.value * (math.pi / 4), // 45 degrees to look like 'X'
            child: const Icon(Icons.add, color: Colors.white, size: 28),
          );
        },
      ),
    );
  }
}

class _MiniActionButton extends StatelessWidget {
  final IconData icon;
  final String label;
  final Color bgColor;
  final Color iconColor;
  final bool isDark;
  final VoidCallback onTap;
  final String heroTag;

  const _MiniActionButton({
    required this.icon,
    required this.label,
    required this.bgColor,
    required this.iconColor,
    required this.isDark,
    required this.onTap,
    required this.heroTag,
  });

  @override
  Widget build(BuildContext context) {
    return Column(
      mainAxisSize: MainAxisSize.min,
      children: [
        FloatingActionButton.small(
          heroTag: heroTag,
          backgroundColor: bgColor,
          foregroundColor: iconColor,
          elevation: isDark ? 4 : 8,
          onPressed: onTap,
          child: Icon(icon, size: 20),
        ),
        const SizedBox(height: 6),
        Container(
          padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 3),
          decoration: BoxDecoration(
            color: isDark
                ? Colors.black.withValues(alpha: 0.65)
                : Colors.white.withValues(alpha: 0.85),
            borderRadius: BorderRadius.circular(6),
            border: Border.all(
              color: isDark
                  ? Colors.white.withValues(alpha: 0.08)
                  : Colors.black.withValues(alpha: 0.04),
            ),
            boxShadow: [
              if (!isDark)
                BoxShadow(
                  color: Colors.black.withValues(alpha: 0.06),
                  blurRadius: 6,
                  offset: const Offset(0, 2),
                ),
            ],
          ),
          child: Text(
            label,
            style: TextStyle(
              fontSize: 10,
              fontWeight: FontWeight.w700,
              color: isDark ? Colors.white : Colors.black87,
              letterSpacing: 0.2,
            ),
          ),
        ),
      ],
    );
  }
}
