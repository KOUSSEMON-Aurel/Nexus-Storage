import 'dart:async';
import 'package:google_sign_in/google_sign_in.dart';
import 'database_service.dart';

class AuthService {
  static final AuthService _instance = AuthService._internal();
  factory AuthService() => _instance;
  
  AuthService._internal() {
    _googleSignIn.onCurrentUserChanged.listen((GoogleSignInAccount? account) {
      _currentUser = account;
      if (account != null) {
        _googleSub = account.id;
        DatabaseService().setKV('google_sub', _googleSub!);
      }
      _userStreamController.add(account);
    });
    
    // Silence sign in on start
    signInSilently();
  }

  // Classic constructor for 6.x.x
  final GoogleSignIn _googleSignIn = GoogleSignIn(
    scopes: [
      'openid',
      'email',
      'profile',
      'https://www.googleapis.com/auth/youtube.force-ssl',
      'https://www.googleapis.com/auth/monitoring.read',
      'https://www.googleapis.com/auth/drive.file',
    ],
  );

  GoogleSignInAccount? _currentUser;
  String? _googleSub;
  String? _lastError;

  final StreamController<GoogleSignInAccount?> _userStreamController = StreamController<GoogleSignInAccount?>.broadcast();
  Stream<GoogleSignInAccount?> get userStream => _userStreamController.stream;

  Future<GoogleSignInAccount?> signInSilently() async {
    try {
      _currentUser = await _googleSignIn.signInSilently();
      if (_currentUser != null) {
        _googleSub = _currentUser!.id;
        _userStreamController.add(_currentUser);
      }
      return _currentUser;
    } catch (e) {
      print('DEBUG: Silent Sign-In Error: $e');
      return null;
    }
  }

  Future<GoogleSignInAccount?> login() async {
    try {
      _lastError = null;
      print('DEBUG: Starting Google Sign-In...');
      final account = await _googleSignIn.signIn();
      
      if (account != null) {
        _currentUser = account;
        _googleSub = account.id;
        await DatabaseService().setKV('google_sub', _googleSub!);
        _userStreamController.add(account);
      }
      
      return account;
    } catch (error) {
      _lastError = error.toString();
      print('DEBUG: Google Sign-In ERROR: $error');
      return null;
    }
  }

  Future<void> logout() async {
    await _googleSignIn.signOut();
    _currentUser = null;
    _googleSub = null;
    _userStreamController.add(null);
  }

  Future<String?> getAccessToken() async {
    if (_currentUser == null) return null;
    final auth = await _currentUser!.authentication;
    return auth.accessToken;
  }

  bool get isAuthenticated => _currentUser != null;
  String? get googleSub => _googleSub;
  String? get userName => _currentUser?.displayName;
  String? get userPhotoUrl => _currentUser?.photoUrl;
  String? get lastError => _lastError;
}
