package com.deviceport.app;

import android.app.Activity;
import android.content.SharedPreferences;
import android.graphics.Color;
import android.os.Bundle;
import android.view.ViewGroup;
import android.widget.Button;
import android.widget.EditText;
import android.widget.LinearLayout;
import android.widget.TextView;
import android.widget.Toast;

/** Settings page for the Android client. Values are stored in SharedPreferences. */
public class SettingsActivity extends Activity {
    public static final String PREF = "deviceport";
    public static final String KEY_SERVER = "server";
    public static final String KEY_NETWORK = "network";
    public static final String KEY_NAME = "device_name";
    public static final String KEY_STATUS = "last_status";

    private EditText server, network, name;

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        if (getActionBar() != null) getActionBar().hide();

        LinearLayout root = new LinearLayout(this);
        root.setOrientation(LinearLayout.VERTICAL);
        root.setBackgroundColor(0xFF1B1E23);
        root.setPadding(48, 110, 48, 48);

        SharedPreferences sp = getSharedPreferences(PREF, MODE_PRIVATE);
        server = field(root, getString(R.string.server_label), sp.getString(KEY_SERVER, "10.0.2.2:9000"));
        network = field(root, getString(R.string.network_label), sp.getString(KEY_NETWORK, "default"));
        name = field(root, getString(R.string.name_label), sp.getString(KEY_NAME, android.os.Build.MODEL));

        Button save = new Button(this);
        save.setText(getString(R.string.save));
        save.setBackgroundColor(0xFF2F8FEA);
        save.setTextColor(Color.WHITE);
        save.setLayoutParams(new LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT,
                ViewGroup.LayoutParams.WRAP_CONTENT));
        save.setOnClickListener(v -> {
            sp.edit()
              .putString(KEY_SERVER, server.getText().toString().trim())
              .putString(KEY_NETWORK, network.getText().toString().trim())
              .putString(KEY_NAME, name.getText().toString().trim())
              .apply();
            Toast.makeText(this, R.string.saved, Toast.LENGTH_SHORT).show();
            finish();
        });
        root.addView(save);
        setContentView(root);
    }

    private EditText field(LinearLayout root, String label, String value) {
        TextView tv = new TextView(this);
        tv.setText(label);
        tv.setTextColor(0xFF8B95A2);
        tv.setTextSize(12);
        tv.setPadding(0, 24, 0, 8);
        root.addView(tv);
        EditText et = new EditText(this);
        et.setText(value);
        et.setTextColor(0xFFD5DBE3);
        et.setBackgroundColor(0xFF22262C);
        et.setPadding(20, 16, 20, 16);
        et.setSingleLine(true);
        root.addView(et, new LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT,
                ViewGroup.LayoutParams.WRAP_CONTENT));
        return et;
    }
}
