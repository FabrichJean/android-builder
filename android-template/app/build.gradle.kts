plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
}

android {
    // namespace interne fixe : garde la classe R accessible depuis MainActivity.
    // L'identité de l'app installée est portée par applicationId (injecté au build).
    namespace = "app.webview"
    compileSdk = 34

    defaultConfig {
        applicationId = "__APPLICATION_ID__"
        minSdk = 24
        targetSdk = 34
        versionCode = __VERSION_CODE__
        versionName = "__VERSION_NAME__"
    }

    buildTypes {
        getByName("debug") {
            // APK debug signé automatiquement avec la clé debug d'Android.
            isMinifyEnabled = false
        }
    }

    // On coupe tout ce qui n'est pas nécessaire pour accélérer le build.
    buildFeatures {
        buildConfig = false
        resValues = false
        shaders = false
        aidl = false
        renderScript = false
    }
    lint {
        checkReleaseBuilds = false
        abortOnError = false
    }

    // Par défaut, AAPT ignore les fichiers commençant par un point (motif ".*").
    // On retire ce motif pour embarquer les dotfiles du dist (ex. .env de
    // flutter_dotenv), sinon ils sont absents de l'APK.
    androidResources {
        ignoreAssetsPattern = "!.svn:!.git:!.ds_store:!*.scc:<dir>_*:!CVS:!thumbs.db:!picasa.ini:!*~"
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
    kotlinOptions {
        jvmTarget = "17"
    }
}

dependencies {
    // Sert un dist embarqué via un domaine virtuel https interne (mode bundle) :
    // gère les chemins absolus des SPA et le chargement des modules ES.
    implementation("androidx.webkit:webkit:1.11.0")
}
