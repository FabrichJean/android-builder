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

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
    kotlinOptions {
        jvmTarget = "17"
    }
}

// Aucune dépendance : WebView et Activity sont dans le framework Android.
dependencies {
}
