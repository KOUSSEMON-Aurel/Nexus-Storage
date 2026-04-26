class NexusException implements Exception {
  final String message;
  final String? code;
  NexusException(this.message, {this.code});
  @override
  String toString() =>
      'NexusException: $message ${code != null ? "($code)" : ""}';
}

class SyncException extends NexusException {
  SyncException(super.message, {super.code});
}

class NetworkException extends NexusException {
  NetworkException(super.message, {super.code});
}

class AuthException extends NexusException {
  AuthException(super.message, {super.code});
}
