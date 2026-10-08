using System.Threading;
using System.Windows;

namespace WiFiWatchdog.Studio;

public partial class App : Application
{
    private Mutex? singleton;
    protected override void OnStartup(StartupEventArgs e)
    {
        singleton = new Mutex(true, @"Local\WiFiWatchdog.Studio.SingleInstance", out var created);
        if (!created)
        {
            MessageBox.Show("WiFi Watchdog Studio 已经在运行。", "WiFi Watchdog Studio",
                MessageBoxButton.OK, MessageBoxImage.Information);
            Shutdown();
            return;
        }
        base.OnStartup(e);
    }

    protected override void OnExit(ExitEventArgs e)
    {
        singleton?.Dispose();
        base.OnExit(e);
    }
}
