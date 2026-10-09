using System.IO;
using System.Threading;
using System.Windows;
using System.Windows.Threading;
using WiFiWatchdog.Studio.Core;

namespace WiFiWatchdog.Studio;

public partial class App : Application
{
    private Mutex? singleton;
    private int crashDialogShown;
    private static string CrashDirectory => Path.Combine(AppPaths.Studio, "crashes");

    protected override void OnStartup(StartupEventArgs e)
    {
        // Crash diagnostics are local only: timestamp + exception type.
        // Never persist raw exception messages or stacks (may contain SSIDs,
        // controller secrets, local IPs or home directory paths).
        DispatcherUnhandledException += OnDispatcherException;
        AppDomain.CurrentDomain.UnhandledException += OnDomainException;
        TaskScheduler.UnobservedTaskException += OnTaskException;

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

    private void OnDispatcherException(object sender, DispatcherUnhandledExceptionEventArgs e)
    {
        CrashJournal.Record(CrashDirectory, "UI", e.Exception);
        if (Interlocked.Exchange(ref crashDialogShown, 1) == 0)
        {
            MessageBox.Show("Studio 遇到意外错误，需要关闭。已在本机保存不包含原始错误内容的诊断编号。\n" +
                            "便携版恢复引擎独立运行，不会被 Studio 的关闭影响。",
                "WiFi Watchdog Studio", MessageBoxButton.OK, MessageBoxImage.Error);
        }
        // Do NOT claim the application is safe to continue after an
        // unhandled WPF error. The dispatcher retains its default behavior.
        e.Handled = false;
    }

    private static void OnDomainException(object sender, UnhandledExceptionEventArgs e)
    {
        if (e.ExceptionObject is Exception exception)
            CrashJournal.Record(CrashDirectory, "Process", exception);
    }

    private static void OnTaskException(object? sender, UnobservedTaskExceptionEventArgs e)
    {
        CrashJournal.Record(CrashDirectory, "BackgroundTask", e.Exception);
        e.SetObserved();
    }

    protected override void OnExit(ExitEventArgs e)
    {
        singleton?.Dispose();
        base.OnExit(e);
    }
}
