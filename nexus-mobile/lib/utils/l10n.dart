class L10n {
  static String get(String key, String lang) {
    String currentLang = lang;
    if (currentLang == 'auto') {
      // Basic detection without PlatformDispatcher for pure static logic
      // In a real app we'd use PlatformDispatcher.instance.locale.languageCode
      // But we can fallback to 'en' or 'fr' based on user preference or hardcode 'en'
      currentLang = 'fr'; // Default to French for this user
    }
    if (currentLang == 'en') {
      return _en[key] ?? key;
    }
    return _fr[key] ?? key;
  }

  static final Map<String, String> _fr = {
    'settings': 'Paramètres',
    'account': 'Compte',
    'display': 'Affichage',
    'theme': 'Thème',
    'language': 'Langue',
    'interaction': 'Interaction',
    'persistent_checkboxes': 'Cases à cocher persistantes',
    'storage_trash': 'Stockage & Corbeille',
    'auto_empty': 'Vidage auto. corbeille',
    'empty_trash_now': 'Vider la corbeille maintenant',
    'security_privacy': 'Sécurité & Confidentialité',
    'zk_encryption': 'Chiffrement Zero-Knowledge',
    'zk_desc':
        'Les fichiers sont automatiquement chiffrés avec XChaCha20-Poly1305. Vos clés sont dérivées de votre identité Google.',
    'camouflage_title': 'Camouflage (Trojan Horse)',
    'camouflage_desc':
        'Les fichiers sont injectés dans des médias MP4 pour rester invisibles.',
    'view_on_github': 'Voir sur GitHub',
    'logout': 'Déconnexion',
    'connect': 'Connexion',
    'my_drive': 'Mon Disque',
    'recent': 'Récents',
    'starred': 'Favoris',
    'trash': 'Corbeille',
    'upload': 'Uploader',
    'activity': 'Activité',
    'database_sync': 'Synchronisation Cloud',
    'auth_required': 'Authentification Requise',
    'please_connect_google':
        'Veuillez connecter votre compte Google dans les paramètres pour uploader des fichiers.',
    'initializing': 'Initialisation...',
    'no_active_tasks': 'Aucune activité en cours',
  };

  static final Map<String, String> _en = {
    'settings': 'Settings',
    'account': 'Account',
    'display': 'Display',
    'theme': 'Theme',
    'language': 'Language',
    'interaction': 'Interaction',
    'persistent_checkboxes': 'Persistent Checkboxes',
    'storage_trash': 'Storage & Trash',
    'auto_empty': 'Auto-Empty Trash',
    'empty_trash_now': 'Empty Trash Now',
    'security_privacy': 'Security & Privacy',
    'zk_encryption': 'Zero-Knowledge Encryption',
    'zk_desc':
        'Files are automatically encrypted with XChaCha20-Poly1305. Your keys are derived from your Google identity.',
    'camouflage_title': 'Camouflage (Trojan Horse)',
    'camouflage_desc':
        'Storage files are injected into MP4 media to remain invisible.',
    'view_on_github': 'View on GitHub',
    'logout': 'Logout',
    'connect': 'Connect',
    'my_drive': 'My Drive',
    'recent': 'Recent',
    'starred': 'Starred',
    'trash': 'Trash',
    'upload': 'Upload',
    'activity': 'Activity',
    'database_sync': 'Cloud Sync',
    'auth_required': 'Authentication Required',
    'please_connect_google':
        'Please connect your Google account in Settings to upload files.',
    'initializing': 'Initializing...',
    'no_active_tasks': 'No active tasks',
  };
}
