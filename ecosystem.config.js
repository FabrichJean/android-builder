module.exports = {
  apps: [
    {
      name: "android-builder",
      script: "./start.sh",
      cwd: "/www/android-builder",
      exec_mode: "fork",
      instances: 1,
      autorestart: true,
      restart_delay: 2000,
      max_restarts: 20,
      min_uptime: "10s",
      env: {
        NODE_ENV: "production",
      },
      out_file: "/www/android-builder/logs/out.log",
      error_file: "/www/android-builder/logs/error.log",
      time: true,
    },
  ],
};
