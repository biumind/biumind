#include "my_application.h"

#include <flutter_linux/flutter_linux.h>
#ifdef GDK_WINDOWING_X11
#include <gdk/gdkx.h>
#endif

#include "flutter/generated_plugin_registrant.h"
#include "desktop_multi_window/desktop_multi_window_plugin.h"

// ─── Repo App 原生子窗口（desktop_multi_window）──────────────────────
// 每个子窗口是独立 Flutter engine，method channel 不共享 —— 新 engine
// 必须重新注册全部插件（window-created 回调），并挂 `biumind/repo_window`
// 自检配通道（尺寸/居中/标题/显示/关闭；插件原生只支持 show/hide）。

static void repo_window_channel_cb(FlMethodChannel* channel,
                                   FlMethodCall* method_call,
                                   gpointer user_data) {
  FlView* view = FL_VIEW(user_data);
  const gchar* method = fl_method_call_get_name(method_call);
  GtkWidget* toplevel = gtk_widget_get_toplevel(GTK_WIDGET(view));

  if (strcmp(method, "configure") == 0) {
    FlValue* args = fl_method_call_get_args(method_call);
    gint w = 1280, h = 800;
    const gchar* title = nullptr;
    if (args != nullptr && fl_value_get_type(args) == FL_VALUE_TYPE_MAP) {
      FlValue* vw = fl_value_lookup_string(args, "width");
      FlValue* vh = fl_value_lookup_string(args, "height");
      FlValue* vt = fl_value_lookup_string(args, "title");
      if (vw != nullptr && fl_value_get_type(vw) == FL_VALUE_TYPE_FLOAT) {
        w = (gint)fl_value_get_float(vw);
      }
      if (vh != nullptr && fl_value_get_type(vh) == FL_VALUE_TYPE_FLOAT) {
        h = (gint)fl_value_get_float(vh);
      }
      if (vt != nullptr && fl_value_get_type(vt) == FL_VALUE_TYPE_STRING) {
        title = fl_value_get_string(vt);
      }
    }
    if (GTK_IS_WINDOW(toplevel)) {
      gtk_window_resize(GTK_WINDOW(toplevel), w, h);
      if (title != nullptr && title[0] != '\0') {
        gtk_window_set_title(GTK_WINDOW(toplevel), title);
      }
      gtk_window_set_position(GTK_WINDOW(toplevel), GTK_WIN_POS_CENTER);
      gtk_window_present(GTK_WINDOW(toplevel));
    }
    fl_method_call_respond(
        method_call,
        FL_METHOD_RESPONSE(fl_method_success_response_new(nullptr)), nullptr);
  } else if (strcmp(method, "close") == 0) {
    if (GTK_IS_WINDOW(toplevel)) {
      gtk_window_close(GTK_WINDOW(toplevel));
    }
    fl_method_call_respond(
        method_call,
        FL_METHOD_RESPONSE(fl_method_success_response_new(nullptr)), nullptr);
  } else {
    fl_method_call_respond(
        method_call,
        FL_METHOD_RESPONSE(fl_method_not_implemented_response_new()), nullptr);
  }
}

static void on_repo_window_created(FlPluginRegistry* registry) {
  fl_register_plugins(registry);
  g_autoptr(FlPluginRegistrar) registrar =
      fl_plugin_registry_get_registrar_for_plugin(registry, "BiumindRepoWindow");
  g_autoptr(FlStandardMethodCodec) codec = fl_standard_method_codec_new();
  g_autoptr(FlMethodChannel) channel = fl_method_channel_new(
      fl_plugin_registrar_get_messenger(registrar), "biumind/repo_window",
      FL_METHOD_CODEC(codec));
  fl_method_channel_set_method_call_handler(
      channel, repo_window_channel_cb, g_object_ref(registry), g_object_unref);
}

// 生命周期：主窗口关闭时连带关闭所有 Repo App 子窗口。
static gboolean on_main_window_delete(GtkWidget* widget, GdkEvent* event,
                                      gpointer user_data) {
  GtkApplication* app = GTK_APPLICATION(user_data);
  GList* windows = gtk_application_get_windows(app);
  for (GList* l = windows; l != nullptr; l = l->next) {
    GtkWindow* w = GTK_WINDOW(l->data);
    if (GTK_WIDGET(w) != widget) {
      gtk_window_close(w);
    }
  }
  return FALSE;  // 继续默认关闭流程
}

struct _MyApplication {
  GtkApplication parent_instance;
  char** dart_entrypoint_arguments;
};

G_DEFINE_TYPE(MyApplication, my_application, GTK_TYPE_APPLICATION)

// Called when first Flutter frame received.
static void first_frame_cb(MyApplication* self, FlView* view) {
  gtk_widget_show(gtk_widget_get_toplevel(GTK_WIDGET(view)));
}

// Implements GApplication::activate.
static void my_application_activate(GApplication* application) {
  MyApplication* self = MY_APPLICATION(application);
  GtkWindow* window =
      GTK_WINDOW(gtk_application_window_new(GTK_APPLICATION(application)));

  // Use a header bar when running in GNOME as this is the common style used
  // by applications and is the setup most users will be using (e.g. Ubuntu
  // desktop).
  // If running on X and not using GNOME then just use a traditional title bar
  // in case the window manager does more exotic layout, e.g. tiling.
  // If running on Wayland assume the header bar will work (may need changing
  // if future cases occur).
  gboolean use_header_bar = TRUE;
#ifdef GDK_WINDOWING_X11
  GdkScreen* screen = gtk_window_get_screen(window);
  if (GDK_IS_X11_SCREEN(screen)) {
    const gchar* wm_name = gdk_x11_screen_get_window_manager_name(screen);
    if (g_strcmp0(wm_name, "GNOME Shell") != 0) {
      use_header_bar = FALSE;
    }
  }
#endif
  if (use_header_bar) {
    GtkHeaderBar* header_bar = GTK_HEADER_BAR(gtk_header_bar_new());
    gtk_widget_show(GTK_WIDGET(header_bar));
    gtk_header_bar_set_title(header_bar, "biumind");
    gtk_header_bar_set_show_close_button(header_bar, TRUE);
    gtk_window_set_titlebar(window, GTK_WIDGET(header_bar));
  } else {
    gtk_window_set_title(window, "biumind");
  }

  gtk_window_set_default_size(window, 1280, 720);
  // 最小窗口尺寸 — 与 macOS (MainFlutterWindow.swift) 一致, 固定表单页按
  // 1024×640 设计, 「滚动条可见 ⟺ 内容真的超长」不变量依赖这个下限。
  GdkGeometry hints;
  hints.min_width = 1024;
  hints.min_height = 640;
  gtk_window_set_geometry_hints(window, nullptr, &hints, GDK_HINT_MIN_SIZE);

  g_autoptr(FlDartProject) project = fl_dart_project_new();
  fl_dart_project_set_dart_entrypoint_arguments(
      project, self->dart_entrypoint_arguments);

  FlView* view = fl_view_new(project);
  GdkRGBA background_color;
  // Background defaults to black, override it here if necessary, e.g. #00000000
  // for transparent.
  gdk_rgba_parse(&background_color, "#000000");
  fl_view_set_background_color(view, &background_color);
  gtk_widget_show(GTK_WIDGET(view));
  gtk_container_add(GTK_CONTAINER(window), GTK_WIDGET(view));

  // Show the window when Flutter renders.
  // Requires the view to be realized so we can start rendering.
  g_signal_connect_swapped(view, "first-frame", G_CALLBACK(first_frame_cb),
                           self);
  gtk_widget_realize(GTK_WIDGET(view));

  fl_register_plugins(FL_PLUGIN_REGISTRY(view));

  // Repo App 子窗口：新 engine 的插件注册 + 自检配通道（见文件顶部）。
  desktop_multi_window_plugin_set_window_created_callback(
      on_repo_window_created);

  // 主窗口关闭 → 连带关闭所有子窗口。
  g_signal_connect(window, "delete-event",
                   G_CALLBACK(on_main_window_delete), application);

  gtk_widget_grab_focus(GTK_WIDGET(view));
}

// Implements GApplication::local_command_line.
static gboolean my_application_local_command_line(GApplication* application,
                                                  gchar*** arguments,
                                                  int* exit_status) {
  MyApplication* self = MY_APPLICATION(application);
  // Strip out the first argument as it is the binary name.
  self->dart_entrypoint_arguments = g_strdupv(*arguments + 1);

  g_autoptr(GError) error = nullptr;
  if (!g_application_register(application, nullptr, &error)) {
    g_warning("Failed to register: %s", error->message);
    *exit_status = 1;
    return TRUE;
  }

  g_application_activate(application);
  *exit_status = 0;

  return TRUE;
}

// Implements GApplication::startup.
static void my_application_startup(GApplication* application) {
  // MyApplication* self = MY_APPLICATION(object);

  // Perform any actions required at application startup.

  G_APPLICATION_CLASS(my_application_parent_class)->startup(application);
}

// Implements GApplication::shutdown.
static void my_application_shutdown(GApplication* application) {
  // MyApplication* self = MY_APPLICATION(object);

  // Perform any actions required at application shutdown.

  G_APPLICATION_CLASS(my_application_parent_class)->shutdown(application);
}

// Implements GObject::dispose.
static void my_application_dispose(GObject* object) {
  MyApplication* self = MY_APPLICATION(object);
  g_clear_pointer(&self->dart_entrypoint_arguments, g_strfreev);
  G_OBJECT_CLASS(my_application_parent_class)->dispose(object);
}

static void my_application_class_init(MyApplicationClass* klass) {
  G_APPLICATION_CLASS(klass)->activate = my_application_activate;
  G_APPLICATION_CLASS(klass)->local_command_line =
      my_application_local_command_line;
  G_APPLICATION_CLASS(klass)->startup = my_application_startup;
  G_APPLICATION_CLASS(klass)->shutdown = my_application_shutdown;
  G_OBJECT_CLASS(klass)->dispose = my_application_dispose;
}

static void my_application_init(MyApplication* self) {}

MyApplication* my_application_new() {
  // Set the program name to the application ID, which helps various systems
  // like GTK and desktop environments map this running application to its
  // corresponding .desktop file. This ensures better integration by allowing
  // the application to be recognized beyond its binary name.
  g_set_prgname(APPLICATION_ID);

  return MY_APPLICATION(g_object_new(my_application_get_type(),
                                     "application-id", APPLICATION_ID, "flags",
                                     G_APPLICATION_NON_UNIQUE, nullptr));
}
