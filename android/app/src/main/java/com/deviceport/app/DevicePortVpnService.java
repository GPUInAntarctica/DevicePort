package com.deviceport.app;

import android.app.Notification;
import android.app.NotificationChannel;
import android.app.NotificationManager;
import android.app.PendingIntent;
import android.content.Intent;
import android.content.SharedPreferences;
import android.net.VpnService;
import android.os.Build;
import android.os.ParcelFileDescriptor;
import android.util.Log;

import org.json.JSONArray;
import org.json.JSONObject;

import java.net.InetAddress;
import java.net.NetworkInterface;
import java.util.Enumeration;

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

        // Promote to foreground immediately: Android requires this shortly
        // after startForegroundService(), before the address probe finishes.
        if (Build.VERSION.SDK_INT >= 29) {
            startForeground(1, notification(),
                    android.content.pm.ServiceInfo.FOREGROUND_SERVICE_TYPE_SPECIAL_USE);
        } else {
            startForeground(1, notification());
        }

        String server = "10.0.2.2:9000";
        String network = "default";
        String name = android.os.Build.MODEL;
        if (intent != null) {
            String v = intent.getStringExtra(EXTRA_SERVER);
            if (v != null && !v.isEmpty()) server = v;
            v = intent.getStringExtra(EXTRA_NETWORK);
            if (v != null && !v.isEmpty()) network = v;
            v = intent.getStringExtra(EXTRA_NAME);
            if (v != null && !v.isEmpty()) name = v;
        }

        final String finalServer = server;
        final String finalNetwork = network;
        final String finalName = name;

        new Thread(() -> startEngine(finalServer, finalNetwork, finalName), "dp-vpn-start").start();
        return START_STICKY;
    }

    /** Runs off the main thread: probe the assigned IP, then establish the tunnel. */
    private void startEngine(String server, String network, String name) {
        SharedPreferences sp = getSharedPreferences(SettingsActivity.PREF, MODE_PRIVATE);
        String privateKey = sp.getString("private_key", "");
        if (privateKey.isEmpty()) {
            try {
                privateKey = Mobile.newIdentity();
                sp.edit().putString("private_key", privateKey).apply();
            } catch (Exception e) {
                fail("newIdentity failed: " + e.getMessage());
                return;
            }
        }

        try {
            JSONObject cfg = new JSONObject();
            cfg.put("server", server);
            cfg.put("network", network);
            cfg.put("name", name);
            cfg.put("private_key", privateKey);
            cfg.put("mtu", 1400);
            cfg.put("ipv6", collectIPv6());

            // Phase 1: the server assigns the virtual IP per stable node ID.
            // Probe with a userspace TUN first because VpnService.Builder must
            // know the address before establish() is called.
            String ip = "";
            String prefix = "";
            Engine probe = null;
            try {
                probe = Mobile.newEngine(cfg.toString(), -1);
                probe.start();
                for (int i = 0; i < 80; i++) { // up to ~12 s
                    JSONObject st = new JSONObject(probe.status());
                    String got = st.optString("ip", "");
                    if (got != null && !got.isEmpty() && !"null".equals(got)) {
                        ip = got;
                        prefix = st.optString("prefix", "10.66.0.0/24");
                        break;
                    }
                    Thread.sleep(150);
                }
                if (ip.isEmpty()) {
                    Log.w(TAG, "server did not assign an IP in time; using fallback");
                    ip = "10.66.0.2";
                    prefix = "10.66.0.0/24";
                }
            } finally {
                if (probe != null) probe.close();
            }
            // Give the server a moment to retire the probe connection before
            // the real engine reconnects with the same identity.
            Thread.sleep(500);

            String routeAddr = prefix;
            int routeBits = 24;
            String[] parts = prefix.split("/");
            if (parts.length == 2) {
                routeAddr = parts[0];
                try { routeBits = Integer.parseInt(parts[1]); } catch (NumberFormatException ignored) {}
            }

            // Phase 2: establish the real VPN with the assigned address.
            Builder builder = new Builder();
            builder.setSession("DevicePort (" + network + ")");
            builder.addAddress(ip, routeBits);
            builder.addRoute(routeAddr, routeBits);
            builder.addDnsServer("8.8.8.8");
            builder.setMtu(1400);
            tun = builder.establish();
            if (tun == null) {
                fail("VpnService.establish() returned null");
                return;
            }

            engine = Mobile.newEngine(cfg.toString(), tun.getFd());
            engine.start();
            // Bind to all interfaces so a browser on the phone and adb forward
            // from a PC can both reach the console.
            engine.startGUI("0.0.0.0:9050");
            sp.edit().putString(SettingsActivity.KEY_STATUS, "running").apply();
            Log.i(TAG, "engine started: " + engine.id() + " at " + ip + " (" + prefix + ")");
        } catch (Exception e) {
            Log.e(TAG, "engine start failed", e);
            fail("error: " + e.getMessage());
        }
    }

    /** Java can read interface addresses even where the app cannot read /proc. */
    private JSONArray collectIPv6() {
        JSONArray out = new JSONArray();
        try {
            Enumeration<NetworkInterface> ifaces = NetworkInterface.getNetworkInterfaces();
            while (ifaces != null && ifaces.hasMoreElements()) {
                NetworkInterface iface = ifaces.nextElement();
                if (!iface.isUp() || iface.isLoopback()) continue;
                Enumeration<InetAddress> addrs = iface.getInetAddresses();
                while (addrs.hasMoreElements()) {
                    InetAddress a = addrs.nextElement();
                    if (a.getAddress().length != 16 || a.isLoopbackAddress() || a.isMulticastAddress()) continue;
                    String s = a.getHostAddress();
                    if (a.isLinkLocalAddress() && iface.getName() != null && !s.contains("%")) {
                        s += "%" + iface.getName();
                    }
                    out.put(s);
                }
            }
        } catch (Exception e) {
            Log.w(TAG, "collectIPv6 failed", e);
        }
        return out;
    }

    private void fail(String message) {
        getSharedPreferences(SettingsActivity.PREF, MODE_PRIVATE)
                .edit().putString(SettingsActivity.KEY_STATUS, message).apply();
        stopSelf();
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
