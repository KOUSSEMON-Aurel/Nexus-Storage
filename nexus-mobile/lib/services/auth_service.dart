import 'dart:async';
import 'dart:convert';
import 'package:google_sign_in/google_sign_in.dart';
import 'package:http/http.dart' as http;
import 'database_service.dart';
import 'logger_service.dart';

class AuthService {
  static final AuthService _instance = AuthService._internal();
  factory AuthService() => _instance;

  final List<String> _scopes = [
    'openid',
    'email',
    'profile',
    'https://www.googleapis.com/auth/youtube.force-ssl',
    'https://www.googleapis.com/auth/monitoring.read',
    'https://www.googleapis.com/auth/drive.file',
  ];

  // Using late final with explicit type
  late final GoogleSignIn _googleSignIn;

  AuthService._internal() {
    _googleSignIn = GoogleSignIn(scopes: _scopes);

    _googleSignIn.onCurrentUserChanged.listen((GoogleSignInAccount? account) {
      _currentUser = account;
      if (account != null) {
        _googleSub = account.id;
        DatabaseService().setKV('google_sub', _googleSub!);
      }
      _userStreamController.add(account);
    });
  }

  GoogleSignInAccount? _currentUser;
  String? _googleSub;
  String? _lastError;
  String? _backgroundToken;
  String? _ytChannelName;
  String? _ytChannelAvatar;

  final StreamController<GoogleSignInAccount?> _userStreamController =
      StreamController<GoogleSignInAccount?>.broadcast();
  Stream<GoogleSignInAccount?> get userStream => _userStreamController.stream;

  void setBackgroundToken(String token) {
    _backgroundToken = token;
  }

  Future<GoogleSignInAccount?> signInSilently() async {
    try {
      _currentUser = await _googleSignIn.signInSilently();
      if (_currentUser != null) {
        _googleSub = _currentUser!.id;
        final savedName = await DatabaseService().getKV('yt_channel_name');
        final savedAvatar = await DatabaseService().getKV('yt_channel_avatar');
        if (savedName != null) _ytChannelName = savedName;
        if (savedAvatar != null) _ytChannelAvatar = savedAvatar;
        _userStreamController.add(_currentUser);
      }
      return _currentUser;
    } catch (e) {
      AppLogger.error('DEBUG: Silent Sign-In Error: $e');
      return null;
    }
  }

  Future<GoogleSignInAccount?> login() async {
    try {
      _lastError = null;
      AppLogger.info('DEBUG: Starting Google Sign-In...');

      final account = await _googleSignIn.signIn();

      if (account != null) {
        _currentUser = account;
        _googleSub = account.id;
        await DatabaseService().setKV('google_sub', _googleSub!);

        // Fetch YouTube Identity
        try {
          final auth = await account.authentication;
          if (auth.accessToken != null) {
            await _fetchYouTubeIdentity(auth.accessToken!);
          }
        } catch (e) {
          AppLogger.warn('DEBUG: Failed to fetch YouTube identity: $e');
        }

        _userStreamController.add(account);
      }

      return account;
    } catch (error) {
      _lastError = error.toString();
      AppLogger.error('DEBUG: Google Sign-In ERROR: $error');
      return null;
    }
  }

  Future<void> _fetchYouTubeIdentity(String token) async {
    try {
      final url = Uri.parse(
        'https://youtube.googleapis.com/youtube/v3/channels?part=snippet&mine=true',
      );
      final response = await http.get(
        url,
        headers: {
          'Authorization': 'Bearer $token',
          'Accept': 'application/json',
        },
      );
      if (response.statusCode == 200) {
        final data = json.decode(response.body);
        if (data['items'] != null && data['items'].isNotEmpty) {
          final snippet = data['items'][0]['snippet'];
          _ytChannelName = snippet['title'];
          _ytChannelAvatar = snippet['thumbnails']['default']['url'];
          if (_ytChannelName != null) {
            await DatabaseService().setKV('yt_channel_name', _ytChannelName!);
          }
          if (_ytChannelAvatar != null) {
            await DatabaseService().setKV(
              'yt_channel_avatar',
              _ytChannelAvatar!,
            );
          }
        }
      }
    } catch (e) {
      AppLogger.warn('DEBUG: Error fetching YouTube channel info: $e');
    }
  }

  Future<void> logout() async {
    await _googleSignIn.signOut();
    _currentUser = null;
    _googleSub = null;
    _backgroundToken = null;
    _ytChannelName = null;
    _ytChannelAvatar = null;
    await DatabaseService().setKV('yt_channel_name', '');
    await DatabaseService().setKV('yt_channel_avatar', '');
    await DatabaseService().setKV('google_sub', '');
    _userStreamController.add(null);
  }

  Future<String?> getAccessToken() async {
    // Optimization: Prioritize the active refreshed user over the static background token
    if (_currentUser != null) {
      try {
        final GoogleSignInAuthentication auth =
            await _currentUser!.authentication;
        return auth.accessToken;
      } catch (e) {
        AppLogger.warn(
          'AuthService: Failed to get fresh token from user session: $e',
        );
        // Fallback to background token if session refresh fails
      }
    }

    if (_backgroundToken != null) return _backgroundToken;
    return null;
  }

  bool get isAuthenticated => _currentUser != null || _backgroundToken != null;
  GoogleSignInAccount? get currentUser => _currentUser;
  String? get googleSub => _googleSub;
  String? get userName => _ytChannelName ?? _currentUser?.displayName;
  String? get userPhotoUrl => _ytChannelAvatar ?? _currentUser?.photoUrl;
  String? get lastError => _lastError;
}
