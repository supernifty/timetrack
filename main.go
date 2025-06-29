package main

import (
	"database/sql"
	_ "embed"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/driver/desktop"

	"github.com/jessevdk/go-flags"
	_ "github.com/mattn/go-sqlite3"
)

var (
	a       fyne.App
	running bool
	latest  = "..."
	desk    desktop.App
	counts  map[string]map[string]int // category app count
	db      string

	// SKIP          = map[string]struct{}{"loginwindow": {}, "ScreenSaverEngine": {}}
	SKIP          = []string{"loginwindow", "ScreenSaverEngine"}
	WAIT          = 10
	MAX_TOP_ITEMS = 3
	UPDATE_MINOR  = 600  // 600 // seconds
	UPDATE_MAJOR  = 3600 // seconds
	SCHEMA        = `
		create table if not exists history (
			occur text, -- yyyy-mm-dd
			category text, -- day week
			app text, -- name of text
			time int -- in minutes
		);

		create table if not exists current (
			category text,
			app text,
			count int -- in segments of WAIT
		)
	`
	//go:embed app-icon.png
	systrayIcon    []byte
	appName        = "TimeTrack"
	PORT           = 4133
	BAR_COUNT      = 8
	BAR_COUNT_WIDE = 28
	MIN_HIST_MINS  = 60.0
)

func initDB() {
	log.Printf("generating schema for %s...", db)
	con, err := sql.Open("sqlite3", db)
	if err != nil {
		log.Fatal(err)
		panic("failed to open db")
	}
	defer con.Close()

	_, err = con.Exec(SCHEMA)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("generating schema from %s: done", db)
}

func write(category string, counts map[string]map[string]int, when time.Time, db string) {
	log.Printf("writing %s...", category)
	con, err := sql.Open("sqlite3", db)
	if err != nil {
		log.Fatal(err)
		panic("failed to write to db")
	}
	defer con.Close()

	for app, count := range counts[category] {
		minutes := count * WAIT / 60
		// log.Printf("%d seconds for %s is %d minutes", count*WAIT, app, minutes)
		if minutes > 0 {
			var updatedWhen time.Time
			if category == "week" {
				updatedWhen = when.AddDate(0, 0, -int(when.Weekday()))
			} else {
				updatedWhen = when
			}
			_, err := con.Exec("insert into history values (?, ?, ?, ?)", fmt.Sprintf("%04d-%02d-%02d", updatedWhen.Year(), updatedWhen.Month(), updatedWhen.Day()), category, app, minutes)
			if err != nil {
				log.Fatal(err)
			}
		}
	}
	log.Printf("writing %s: done", category)
}

func save(counts map[string]map[string]int, lastTime time.Time, db string) {
	// log.Printf("saving...")
	con, err := sql.Open("sqlite3", db)
	if err != nil {
		log.Fatal(err)
	}
	defer con.Close()

	_, err = con.Exec("delete from current")
	if err != nil {
		log.Fatal(err)
	}
	for cat, apps := range counts {
		for app, count := range apps {
			_, err := con.Exec("insert into current values (?, ?, ?)", cat, app, count)
			if err != nil {
				log.Fatal(err)
			}
		}
	}
	_, err = con.Exec("insert into current values (?, ?, ?)", "last_time", fmt.Sprintf("%04d-%02d-%02d", lastTime.Year(), lastTime.Month(), lastTime.Day()), 0)
	if err != nil {
		log.Fatal(err)
	}
	// log.Printf("saving: done")
}

func load(counts map[string]map[string]int, db string) (time.Time, error) {
	log.Printf("loading...")
	con, err := sql.Open("sqlite3", db)
	if err != nil {
		return time.Time{}, err
	}
	defer con.Close()

	var lastTime time.Time
	rows, err := con.Query("select category, app, count from current")
	if err != nil {
		return time.Time{}, err
	}
	defer rows.Close()

	for rows.Next() {
		var category, app string
		var count int
		if err := rows.Scan(&category, &app, &count); err != nil {
			return time.Time{}, err
		}
		if category == "last_time" {
			ymd := make([]int, 3)
			fmt.Sscanf(app, "%d-%d-%d", &ymd[0], &ymd[1], &ymd[2])
			lastTime = time.Date(ymd[0], time.Month(ymd[1]), ymd[2], 0, 0, 0, 0, time.UTC)
			continue
		}
		if counts[category] == nil {
			counts[category] = make(map[string]int)
		}
		counts[category][app] = count
	}
	log.Printf("loading: done.")
	return lastTime, nil
}

func currentApp() (string, error) {
	output, err := exec.Command("bash", "-c", "lsappinfo | grep \"$(lsappinfo front)\"").Output()
	if err != nil {
		return "", err
	}
	app := string(output)
	// take the first word after the first space and remove quotes
	appname := strings.Replace(strings.Split(strings.Trim(app, " "), " ")[1], "\"", "", -1)
	return appname, nil
}

func isNewDay(old, new time.Time) bool {
	return new.YearDay() > old.YearDay()
}

func isNewWeek(old, new time.Time) bool {
	return new.YearDay()-int(new.Weekday()) > old.YearDay()-int(old.Weekday())
}

func notify(app string, duration string, major bool) {
	latest = fmt.Sprintf("%s: %s", app, duration)
	if major {
		fyne.Do(func() {
			a.SendNotification(fyne.NewNotification(app, duration))
		})
	}
	updateMenu()
	log.Println(latest)
}

func process(current string, counts map[string]map[string]int) {
	counts["day"][current]++
	counts["week"][current]++

	// 10 minute intervals for day
	seconds := counts["day"][current] * WAIT
	if seconds < UPDATE_MAJOR {
		if seconds%UPDATE_MINOR == 0 {
			notify(current, fmt.Sprintf("%d minutes today", seconds/60), false)
		}
	} else if seconds%UPDATE_MAJOR == 0 {
		notify(current, fmt.Sprintf("%d hour%s today", seconds/3600, pluralise(seconds/3600)), true)
	}

	// same for week but only hours
	seconds = counts["week"][current] * WAIT
	if seconds >= UPDATE_MAJOR && seconds%UPDATE_MAJOR == 0 {
		notify(current, fmt.Sprintf("%d hour%s this week", seconds/3600, pluralise(seconds/3600)), true)
	}
}

func pluralise(i int) string {
	if i == 1 {
		return ""
	}
	return "s"
}

func updateMenu() {
	fyne.Do(func() {
		menu := makeMenu()
		desk.SetSystemTrayMenu(menu)
	})
}

func mainLoop(db string) {
	log.Printf("writing to %s...", db)
	counts = map[string]map[string]int{
		"day":  make(map[string]int),
		"week": make(map[string]int),
	}

	lastTime, _ := load(counts, db) // TODO handle err
	if lastTime.IsZero() {
		lastTime = time.Now()
	}

	updateMenu() // doesn't seem to work TODO

	count := 0
	for {
		count++
		// log.Printf("count is %d", count)

		newTime := time.Now()
		if isNewWeek(lastTime, newTime) { // both a new day and a new week
			write("week", counts, lastTime, db)   // save
			counts["week"] = make(map[string]int) // reset
			write("day", counts, lastTime, db)    // save
			counts["day"] = make(map[string]int)  // reset
			lastTime = newTime
		} else if isNewDay(lastTime, newTime) {
			write("day", counts, lastTime, db)   // save
			counts["day"] = make(map[string]int) // reset
			lastTime = newTime
		}

		current, err := currentApp()
		if err != nil {
			panic(err)
		}

		// skip if not interested
		if !contains(SKIP, current) {
			process(current, counts) // update counts, notify
			if count%6 == 0 {        // every minute
				save(counts, lastTime, db) // save to database
				updateMenu()
			}
		}

		time.Sleep(time.Duration(WAIT) * time.Second)
		if !running {
			break
		}
		//log.Printf("mainLoop: continuing...")
	}

	// log.Println("done")
}

func contains(slice []string, item string) bool {
	for _, a := range slice {
		if a == item {
			return true
		}
	}
	return false
}

func startGUI() {
	var ok bool
	a = app.NewWithID("org.supernifty.timetrack")
	if desk, ok = a.(desktop.App); ok {
		systemTrayIcon := fyne.NewStaticResource("icon", systrayIcon)
		desk.SetSystemTrayIcon(systemTrayIcon)
		menu := makeMenu()
		desk.SetSystemTrayMenu(menu)

		running = true
		go mainLoop(db)

		a.Run()
	}

	running = false
	log.Printf("gui: done")
}

func toDuration(count int) string {
	minutes := count * WAIT / 60
	if minutes <= 1 {
		return "..."
	}
	if minutes < 60 {
		return fmt.Sprintf("%d minutes", minutes)
	}
	hours := float32(count*WAIT) / float32(3600)
	return fmt.Sprintf("%.1f hours", hours)
}

func addTopItems(category string, items []*fyne.MenuItem) []*fyne.MenuItem {
	type kv struct {
		Key   string
		Value int
	}

	var ss []kv
	for k, v := range counts[category] {
		ss = append(ss, kv{k, v})
	}

	sort.Slice(ss, func(i, j int) bool {
		return ss[i].Value > ss[j].Value
	})

	c := MAX_TOP_ITEMS
	for _, kv := range ss {
		items = append(items, fyne.NewMenuItem(fmt.Sprintf("▪ %s: %s", kv.Key, toDuration(kv.Value)), nil)) // TODO click to see app specific
		if c == 0 {
			break
		}
		c -= 1
	}
	return items
}

func makeMenu() *fyne.Menu {
	// overall totals
	daily := 0
	for _, v := range counts["day"] {
		daily += v
	}

	weekly := 0
	for _, v := range counts["week"] {
		weekly += v
	}

	items := []*fyne.MenuItem{
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem(fmt.Sprintf("Latest: %s", latest), nil),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem(fmt.Sprintf("Today: %s", toDuration(daily)), func() {
			openBrowser(fmt.Sprintf("http://localhost:%d/chart", PORT))
		}),
	}
	items = addTopItems("day", items)

	items = append(items,
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem(fmt.Sprintf("This week: %s", toDuration(weekly)), func() {
			openBrowser(fmt.Sprintf("http://localhost:%d/chart", PORT))
		}),
	)
	items = addTopItems("week", items)

	items = append(items,
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Quit", func() {
			a.Quit()
		}))

	return fyne.NewMenu(
		appName,
		items...)
}

func dbFile() string {
	configDir, err := os.UserConfigDir()
	if err != nil {
		panic(err)
	}

	appConfigDir := filepath.Join(configDir, "TimeTrack")
	fmt.Println("Config directory:", appConfigDir)
	return filepath.Join(appConfigDir, "timetrack.sqlite")
}

// charts

func openBrowser(url string) {
	exec.Command("open", url).Start() // macOS
}

func chartVals(rows *sql.Rows) ([]string, []float64, []string, int) {
	var apps []string
	var values []float64
	var text []string
	index := 0
	total := 0
	for rows.Next() {
		var app string
		var count int
		rows.Scan(&app, &count)
		hours := float64(count*WAIT) / float64(3600) // convert to hours
		total += count
		if index < BAR_COUNT {
			apps = append(apps, app)
			values = append(values, hours)
			text = append(text, toDuration(count))
		}
		index++
	}
	return apps, values, text, total
}

// callback from web request
func chartHandler(w http.ResponseWriter, _ *http.Request) {
	con, err := sql.Open("sqlite3", db)
	if err != nil {
		log.Fatalf("failed to open database: %v", err)
	}

	rows, err := con.Query("SELECT app, count FROM current where category = 'day' order by count desc")
	if err != nil {
		http.Error(w, "Query error", 500)
		return
	}
	defer rows.Close()
	apps, values, text, total := chartVals(rows)
	dayLabelsJSON, _ := json.Marshal(apps)
	dayValuesJSON, _ := json.Marshal(values)
	dayTextJSON, _ := json.Marshal(text)
	dayTotal := total

	rows, err = con.Query("SELECT app, count FROM current where category = 'week' order by count desc")
	if err != nil {
		http.Error(w, "Query error", 500)
		return
	}
	defer rows.Close()

	apps, values, text, total = chartVals(rows)
	weekLabelsJSON, _ := json.Marshal(apps)
	weekValuesJSON, _ := json.Marshal(values)
	weekTextJSON, _ := json.Marshal(text)
	weekTotal := total

	// daily historical
	today := time.Now()
	past := today.AddDate(0, 0, -BAR_COUNT_WIDE)
	rows, err = con.Query("SELECT occur, app, time FROM history where category = 'day' and occur >= '%s' order by time, app desc", past.Format("2006-01-02"))
	if err != nil {
		http.Error(w, "Query error", 500)
		return
	}
	defer rows.Close()

	occurs := make(map[string]bool)             // each day
	histApps := make(map[string]bool)           // each app
	histMins := make(map[string]map[string]int) // app -> day -> minutes

	for rows.Next() {
		var occur, histApp string
		var minutes int
		rows.Scan(&occur, &histApp, &minutes)

		occurs[occur] = true
		histApps[histApp] = true

		if histMins[histApp] == nil {
			histMins[histApp] = make(map[string]int)
		}
		histMins[histApp][occur] = minutes
	}

	var dayList []string
	for m := range occurs {
		dayList = append(dayList, m)
		//dayList = append([]string{m}, dayList...)
	}

	dayhistData := make(map[string][]float64)

	for p := range histApps {
		for _, m := range dayList {
			dayhistData[p] = append(dayhistData[p], float64(histMins[p][m])/float64(60)) // missing data = 0, app -> [val for each day]
		}
	}

	//filteredApps := make([]string, 0)
	filteredApps := make(map[string][]float64)
	otherExists := false
	for app, values := range dayhistData {
		total := 0.0
		for _, v := range values {
			total += v
		}
		if total >= float64(MIN_HIST_MINS)/float64(60) {
			filteredApps[app] = dayhistData[app]
		} else {
			if otherExists {
				for i := 0; i < len(filteredApps["Other"]); i++ {
					filteredApps["Other"][i] = filteredApps["Other"][i] + dayhistData[app][i]
				}
			} else {
				filteredApps["Other"] = dayhistData[app]
				otherExists = true
			}
		}
	}

	dayHistX, _ := json.Marshal(dayList)      // days to show
	dayHistY, _ := json.Marshal(filteredApps) // each app -> [val for each day]

	// weekly historical
	past = today.AddDate(0, 0, -BAR_COUNT_WIDE*7)
	rows, err = con.Query("SELECT occur, app, time FROM history where category = 'week' and occur >= '%s' order by time, app desc", past.Format("2006-01-02"))
	if err != nil {
		http.Error(w, "Query error", 500)
		return
	}
	defer rows.Close()

	occurs = make(map[string]bool)             // each day
	histApps = make(map[string]bool)           // each app
	histMins = make(map[string]map[string]int) // app -> day -> minutes

	for rows.Next() {
		var occur, histApp string
		var minutes int
		rows.Scan(&occur, &histApp, &minutes)

		occurs[occur] = true
		histApps[histApp] = true

		if histMins[histApp] == nil {
			histMins[histApp] = make(map[string]int)
		}
		histMins[histApp][occur] = minutes
	}

	var weekList []string
	for m := range occurs {
		weekList = append(weekList, m)
	}

	weekHistData := make(map[string][]float64)

	for p := range histApps {
		for _, m := range weekList {
			weekHistData[p] = append(weekHistData[p], float64(histMins[p][m])/float64(60)) // missing data = 0, app -> [val for each day]
		}
	}

	//filteredProducts := make([]string, 0)
	filteredApps = make(map[string][]float64)
	otherExists = false
	for app, values := range weekHistData {
		total := 0.0
		for _, v := range values {
			total += v
		}
		if total >= float64(MIN_HIST_MINS)/float64(60) {
			filteredApps[app] = weekHistData[app]
		} else {
			if otherExists {
				for i := 0; i < len(filteredApps["Other"]); i++ {
					filteredApps["Other"][i] = filteredApps["Other"][i] + weekHistData[app][i]
				}
			} else {
				filteredApps["Other"] = weekHistData[app]
				otherExists = true
			}
		}
	}

	weekHistX, _ := json.Marshal(weekList)     // days to show
	weekHistY, _ := json.Marshal(filteredApps) // each app -> [val for each day]

	path, err := getResourcePath("templates/graph.html")
	if err != nil {
		log.Fatal(err)
	}

	tmpl := template.Must(template.ParseFiles(path))
	tmpl.Execute(w, map[string]template.JS{
		"DayLabels":  template.JS(dayLabelsJSON),
		"DayValues":  template.JS(dayValuesJSON),
		"DayText":    template.JS(dayTextJSON),
		"DayTitle":   template.JS(fmt.Sprintf("Today: %s", toDuration(dayTotal))),
		"WeekLabels": template.JS(weekLabelsJSON),
		"WeekValues": template.JS(weekValuesJSON),
		"WeekText":   template.JS(weekTextJSON),
		"WeekTitle":  template.JS(fmt.Sprintf("This Week: %s", toDuration(weekTotal))),
		"DayHistX":   template.JS(dayHistX),
		"DayHistY":   template.JS(dayHistY),
		"WeekHistX":  template.JS(weekHistX),
		"WeekHistY":  template.JS(weekHistY),
	})
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func getResourcePath(relativePath string) (string, error) {
	exePath, err := os.Executable()
	if err != nil {
		return "", err
	}
	exeRealPath, err := filepath.EvalSymlinks(exePath)
	if err != nil {
		return "", err
	}
	// From /Contents/MacOS/YourApp -> up to /Contents
	contentsDir := filepath.Dir(filepath.Dir(exeRealPath))
	resourcePath := filepath.Join(contentsDir, "Resources", relativePath)

	// dev fallback
	if !fileExists(resourcePath) {
		return relativePath, nil
	}

	return resourcePath, nil
}

func main() {
	var opts struct {
		DB      string `long:"db" description:"db to write to" required:"false"`
		Verbose bool   `long:"verbose" description:"more logging"`
	}
	_, err := flags.Parse(&opts)
	if err != nil {
		log.Fatal(err)
	}

	if opts.Verbose {
		log.SetFlags(log.LstdFlags | log.Lshortfile)
	} else {
		log.SetFlags(log.LstdFlags)
	}

	if opts.DB == "" {
		opts.DB = dbFile()
	}

	go func() {
		path, err := getResourcePath("static")
		if err != nil {
			log.Fatal(err)
		}
		http.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir(path))))
		http.HandleFunc("/chart", chartHandler)
		log.Printf("Serving at http://localhost:%d/ v1", PORT)
		http.ListenAndServe(fmt.Sprintf(":%d", PORT), nil)
	}()

	db = opts.DB
	initDB()
	startGUI()
}
