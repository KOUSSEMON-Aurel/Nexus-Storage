class NexusException implements Exception {
  final String message;
  final String? code;
  NexusException(this.message, {this.code});
  @override
  String toString() => 'NexusException: $message ${code != null ? "($code)" : ""}';
}

class SyncException extends NexusException {
  SyncException(String message, {String? code}) : super(message, code: code);
}

class NetworkException extends NexusException {
  NetworkException(String message, {String? code}) : super(message, code: code);
}

class AuthException extends NexusException {
  AuthException(String message, {String? code}) : super(message, code: code);
}
