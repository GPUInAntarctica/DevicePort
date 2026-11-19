package com.deviceport.app;

import android.app.Activity;
import android.content.Intent;
import android.content.SharedPreferences;
import android.net.VpnService;
import android.os.Bundle;
import android.os.Handler;
import android.os.Looper;
import android.view.ViewGroup;
import android.webkit.WebView;
import android.widget.Button;
import android.widget.LinearLayout;
import android.widget.TextView;
import android.widget.Toast;

import java.io.IOException;
import java.net.InetAddress;
import java.net.InetSocketAddress;
import java.net.NetworkInterface;
import java.net.Socket;
import java.util.Enumeration;

/** Main UI: start/stop the VPN and show the embedded bilingual web control panel. */
public class MainActivity extends Activity {
    private static final int REQUEST_VPN = 7;
    private static final String CONSOLE_URL = "http://127.0.0.1:9050/";
    private final Handler handler = new Handler(Looper.getMainLooper());
    private TextView status;
    private WebView web;

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        if (getActionBar() != null) getActionBar().hide();

        LinearLayout root = new LinearLayout(this);
        root.setOrientation(LinearLayout.VERTICAL);
        root.setBackgroundColor(0xFF0B1020);

        status = new TextView(this);
        status.setTextColor(0xFF98A6CC);
        status.setText(getString(R.string.status_idle));
        status.setPadding(24, 92, 24, 12);
        root.addView(status);

        LinearLayout buttons = new LinearLayout(this);
        buttons.setOrientation(LinearLayout.HORIZONTAL);
        buttons.setPadding(16, 0, 16, 12);

        Button start = new Button(this);
        start.setText(getString(R.string.start_vpn));
        start.setLayoutParams(new LinearLayout.LayoutParams(0,
                ViewGroup.LayoutParams.WRAP_CONTENT, 1));
        start.setOnClickListener(v -> startVpn());
        buttons.addView(start);

        Button stop = new Button(this);
        stop.setText(getString(R.string.stop_vpn));
        stop.setLayoutParams(new LinearLayout.LayoutParams(0,
                ViewGroup.LayoutParams.WRAP_CONTENT, 1));
        stop.setOnClickListener(v -> {
            stopService(new Intent(this, DevicePortVpnService.class));
            prefs().edit().putString(SettingsActivity.KEY_STATUS, "").apply();
            status.setText(getString(R.string.status_idle));
            showPlaceholder();
        });
        buttons.addView(stop);

        Button settings = new Button(this);
        settings.setText(getString(R.string.settings));
        settings.setLayoutParams(new LinearLayout.LayoutParams(0,
                ViewGroup.LayoutParams.WRAP_CONTENT, 1));
        settings.setOnClickListener(v -> startActivity(new Intent(this, SettingsActivity.class)));
        buttons.addView(settings);
        root.addView(buttons);

        web = new WebView(this);
        WebSettingsProvider.configure(web);
        root.addView(web, new LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, 0, 1));

        setContentView(root);
        // The console is served by the VPN service, which is not running yet.
        // Show a friendly hint instead of a WebView ERR_CONNECTION_REFUSED page.
        showPlaceholder();
    }

    private static class WebSettingsProvider {
        static void configure(WebView web) {
            android.webkit.WebSettings s = web.getSettings();
            s.setJavaScriptEnabled(true);
            s.setDomStorageEnabled(true);
            s.setLoadWithOverviewMode(true);
            s.setUseWideViewPort(true);
            web.setBackgroundColor(0xFF0B1020);
        }
    }

    @Override
    protected void onResume() {
        super.onResume();
        String st = prefs().getString(SettingsActivity.KEY_STATUS, "");
        if (st.startsWith("error:")) {
            status.setText(st);
            return;
        }
        // The local GUI port is the source of truth: a force-stop or reboot can
        // leave a stale "running" flag while the service is actually gone.
        new Thread(() -> {
            boolean alive = portOpen();
            handler.post(() -> {
                if (alive) {
                    status.setText(getString(R.string.status_url, localIP()));
                    loadConsoleWhenReady();
                } else {
                    status.setText(R.string.status_idle);
                    showPlaceholder();
                }
            });
        }, "dp-state-check").start();
    }

    private SharedPreferences prefs() {
        return getSharedPreferences(SettingsActivity.PREF, MODE_PRIVATE);
    }

    /** Best-effort local IP so users can open the console from another app. */
    private String localIP() {
        try {
            Enumeration<NetworkInterface> ifaces = NetworkInterface.getNetworkInterfaces();
            while (ifaces != null && ifaces.hasMoreElements()) {
                Enumeration<InetAddress> addrs = ifaces.nextElement().getInetAddresses();
                while (addrs.hasMoreElements()) {
                    InetAddress a = addrs.nextElement();
                    if (!a.isLoopbackAddress() && !a.isLinkLocalAddress() && a.getAddress().length == 4) {
                        return a.getHostAddress();
                    }
                }
            }
        } catch (Exception ignored) {
        }
        return "127.0.0.1";
    }

    /** Placeholder shown while the VPN service (and its GUI server) is not running. */
    private void showPlaceholder() {
        String html = "<html><body style='margin:0;background:#0B1020;color:#D5DBE3;"
                + "font-family:sans-serif;padding:36px 24px'>"
                + "<div style='font-size:19px;color:#8B95A2'>DevicePort</div>"
                + "<div style='font-size:26px;margin-top:18px'>"
                + getString(R.string.placeholder_title) + "</div>"
                + "<div style='font-size:16px;color:#8B95A2;margin-top:14px;line-height:1.6'>"
                + getString(R.string.placeholder_body) + "</div></body></html>";
        web.loadDataWithBaseURL(null, html, "text/html", "utf-8", null);
    }

    /**
     * The GUI server starts slightly after the service. Poll the local port
     * off the UI thread so we never render a connection-error page.
     */
    private void loadConsoleWhenReady() {
        new Thread(() -> {
            for (int i = 0; i < 60; i++) {
                if (portOpen()) {
                    handler.post(() -> {
                        status.setText(getString(R.string.status_url, localIP()));
                        web.loadUrl(CONSOLE_URL);
                    });
                    return;
                }
                try {
                    Thread.sleep(250);
                } catch (InterruptedException e) {
                    return;
                }
            }
            handler.post(() -> status.setText(R.string.gui_timeout));
        }, "dp-console-poll").start();
    }

    private boolean portOpen() {
        try (Socket s = new Socket()) {
            s.connect(new InetSocketAddress("127.0.0.1", 9050), 200);
            return true;
        } catch (IOException e) {
            return false;
        }
    }

    private void startVpn() {
        Intent prepare = VpnService.prepare(this);
        if (prepare != null) {
            startActivityForResult(prepare, REQUEST_VPN);
        } else {
            launchService();
        }
    }

    private void launchService() {
        Intent i = new Intent(this, DevicePortVpnService.class);
        i.putExtra(DevicePortVpnService.EXTRA_SERVER, prefs().getString(SettingsActivity.KEY_SERVER, "10.0.2.2:9000"));
        i.putExtra(DevicePortVpnService.EXTRA_NETWORK, prefs().getString(SettingsActivity.KEY_NETWORK, "default"));
        i.putExtra(DevicePortVpnService.EXTRA_NAME, prefs().getString(SettingsActivity.KEY_NAME, android.os.Build.MODEL));
        startForegroundService(i);
        status.setText(getString(R.string.status_starting));
        loadConsoleWhenReady();
    }

    @Override
    protected void onActivityResult(int request, int result, Intent data) {
        super.onActivityResult(request, result, data);
        if (request == REQUEST_VPN) {
            if (result == RESULT_OK) launchService();
            else Toast.makeText(this, R.string.vpn_denied, Toast.LENGTH_SHORT).show();
        }
    }
}
