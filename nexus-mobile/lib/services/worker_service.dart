import 'dart:async';
import 'database_service.dart';
import 'nexus_service.dart';
import 'nexus_ffmpeg.dart';

class WorkerService {
  static final WorkerService _instance = WorkerService._internal();
  factory WorkerService() => _instance;
  WorkerService._internal();

  final DatabaseService _db = DatabaseService();
  final NexusService _nexus = NexusService();
  
  bool _isRunning = false;
  Timer? _timer;

  void start() {
    if (_isRunning) return;
    _isRunning = true;
    _timer = Timer.periodic(const Duration(seconds: 10), (timer) => _processTasks());
  }

  void stop() {
    _timer?.cancel();
    _isRunning = false;
  }

  Future<void> _processTasks() async {
    // 1. Fetch pending tasks from DB
    final tasks = await _db.getPendingTasks(); // We'll need to implement this correctly in DB service
    
    // 2. Process them one by one
    // For now, this is a placeholder for the full job queue logic
    // we'll implement in the next iteration.
  }
}
