package com.deviceport.app;

import android.app.Notification;
import android.app.NotificationChannel;
import android.app.NotificationManager;
import android.app.PendingIntent;
import android.app.Service;
import android.content.Intent;
import android.content.SharedPreferences;
import android.net.VpnService;
import android.os.Build;
import android.os.ParcelFileDescriptor;
import android.util.Log;

import org.json.JSONObject;

import mobile.Engine;
import mobile.Mobile;

/**
 * Android VPN service. It creates the TUN interface through VpnService and
 * hands its file descriptor to the Go engine bundled in the gomobile AAR.
 */
public class DevicePortVpnService extends VpnService {
    public static final String TAG = "DevicePortVPN";
    public static final String EXTRA_SERVER = "server";
    public static final String EXTRA_NETWORK = "network";
    public static final String EXTRA_NAME = "name";

    private Engine engine;
    private ParcelFileDescriptor tun;

    @Override
    public int onStartCommand(Intent intent, int flags, int startId) {
        if (engine != null) return START_STICKY;

        String server = intent.getStringExtra(EXTRA_SERVER);
        String network = intent.getStringExtra(EXTRA_NETWORK);
        String name = intent.getStringExtra(EXTRA_NAME);
        if (server == null || server.isEmpty()) server = "127.0.0.1:9000";
        if (network == null || network.isEmpty()) network = "default";
        if (name == null || name.isEmpty()) name = android.os.Build.MODEL;

        SharedPreferences sp = getSharedPreferences("deviceport", MODE_PRIVATE);
        String privateKey = sp.getString("private_key", "");
        if (privateKey.isEmpty()) {
            try {
                privateKey = Mobile.newIdentity();
                sp.edit().putString("private_key", privateKey).apply();
            } catch (Exception e) {
                Log.e(TAG, "newIdentity failed", e);
                stopSelf();
                return START_NOT_STICKY;
            }
        }

        // The virtual address must be known before the Go engine learns the
        // server-assigned IP. Run the server with --subnet 10.66.0.0/24.
        Builder builder = new Builder();
        builder.setSession("DevicePort (" + network + ")");
        builder.addAddress("10.66.0.2", 24);
        builder.addRoute("10.66.0.0", 24);
        builder.addDnsServer("8.8.8.8");
        builder.setMtu(1400);
        tun = builder.establish();
        if (tun == null) {
            Log.e(TAG, "VpnService.establish() returned null");
            stopSelf();
            return START_NOT_STICKY;
        }

        try {
            JSONObject cfg = new JSONObject();
            cfg.put("server", server);
            cfg.put("network", network);
            cfg.put("name", name);
            cfg.put("private_key", privateKey);
            cfg.put("mtu", 1400);
            engine = Mobile.newEngine(cfg.toString(), tun.getFd());
            engine.start();
            // Bind to all interfaces so a browser on the phone (http://<phone-ip>:9050)
            // and `adb forward tcp:9050 tcp:9050` from a PC can reach the console.
            engine.startGUI("0.0.0.0:9050");
            getSharedPreferences(SettingsActivity.PREF, MODE_PRIVATE)
                    .edit().putString(SettingsActivity.KEY_STATUS, "running").apply();
            Log.i(TAG, "engine started: " + engine.id());
        } catch (Exception e) {
            Log.e(TAG, "engine start failed", e);
            getSharedPreferences(SettingsActivity.PREF, MODE_PRIVATE)
                    .edit().putString(SettingsActivity.KEY_STATUS, "error: " + e.getMessage()).apply();
            stopSelf();
            return START_NOT_STICKY;
        }

        if (Build.VERSION.SDK_INT >= 29) {
            startForeground(1, notification(),
                    android.content.pm.ServiceInfo.FOREGROUND_SERVICE_TYPE_SPECIAL_USE);
        } else {
            startForeground(1, notification());
        }
        return START_STICKY;
    }

    private Notification notification() {
        Intent i = new Intent(this, MainActivity.class);
        PendingIntent pi = PendingIntent.getActivity(this, 0, i,
                PendingIntent.FLAG_IMMUTABLE | PendingIntent.FLAG_UPDATE_CURRENT);
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            NotificationChannel ch = new NotificationChannel("dp", "DevicePort",
                    NotificationManager.IMPORTANCE_LOW);
            ((NotificationManager) getSystemService(NOTIFICATION_SERVICE)).createNotificationChannel(ch);
            return new Notification.Builder(this, "dp")
                    .setContentTitle(getString(R.string.app_name))
                    .setContentText(getString(R.string.vpn_started))
                    .setSmallIcon(android.R.drawable.ic_dialog_info)
                    .setContentIntent(pi).build();
        }
        return new Notification.Builder(this)
                .setContentTitle(getString(R.string.app_name))
                .setContentText(getString(R.string.vpn_started))
                .setSmallIcon(android.R.drawable.ic_dialog_info)
                .setContentIntent(pi).build();
    }

    @Override
    public void onDestroy() {
        if (engine != null) { engine.close(); engine = null; }
        if (tun != null) { try { tun.close(); } catch (Exception ignored) {} tun = null; }
        getSharedPreferences(SettingsActivity.PREF, MODE_PRIVATE)
                .edit().putString(SettingsActivity.KEY_STATUS, "").apply();
        super.onDestroy();
    }
}
