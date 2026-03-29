#[derive(Debug, thiserror::Error)]
pub enum NexusError {
    #[error("Daemon inaccessible sur {url}. Lance `nexus daemon start`.")]
    DaemonUnreachable { url: String },

    #[error("Non authentifié. Lance `nexus auth login`.")]
    NotAuthenticated,

    #[error("Quota épuisé ({used}/{total}). Reset dans {reset}.")]
    QuotaExceeded { used: u32, total: u32, reset: u64 },

    #[error("Fichier non trouvé (ID ou Query) : {id}")]
    FileNotFound { id: String },

    #[error("Erreur API YouTube : {msg}")]
    YouTubeError { msg: String },

    #[error("Erreur inattendue de l'API Daemon: {0}")]
    ApiError(String),

    #[error(transparent)]
    Http(#[from] reqwest::Error),
}
