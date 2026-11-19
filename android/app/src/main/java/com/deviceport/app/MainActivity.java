package com.deviceport.app;

import android.app.Activity;
import android.content.Intent;
import android.content.SharedPreferences;
import android.net.VpnService;
import android.os.Bundle;
import android.view.ViewGroup;
import android.webkit.WebSettings;
import android.webkit.WebView;
import android.widget.Button;
import android.widget.LinearLayout;
import android.widget.TextView;
import android.widget.Toast;

/** Main UI: start/stop the VPN and show the embedded bilingual web control panel. */
public class MainActivity extends Activity {
    private static final int REQUEST_VPN = 7;
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
            status.setText(getString(R.string.status_idle));
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
        WebSettings s = web.getSettings();
        s.setJavaScriptEnabled(true);
        s.setDomStorageEnabled(true);
        s.setLoadWithOverviewMode(true);
        s.setUseWideViewPort(true);
        web.setBackgroundColor(0xFF0B1020);
        root.addView(web, new LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, 0, 1));

        setContentView(root);
        web.loadUrl("http://127.0.0.1:9050/");
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
        SharedPreferences sp = getSharedPreferences(SettingsActivity.PREF, MODE_PRIVATE);
        Intent i = new Intent(this, DevicePortVpnService.class);
        i.putExtra(DevicePortVpnService.EXTRA_SERVER, sp.getString(SettingsActivity.KEY_SERVER, "10.0.2.2:9000"));
        i.putExtra(DevicePortVpnService.EXTRA_NETWORK, sp.getString(SettingsActivity.KEY_NETWORK, "default"));
        i.putExtra(DevicePortVpnService.EXTRA_NAME, sp.getString(SettingsActivity.KEY_NAME, android.os.Build.MODEL));
        startForegroundService(i);
        status.setText(getString(R.string.status_running));
        web.postDelayed(() -> web.loadUrl("http://127.0.0.1:9050/"), 800);
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
