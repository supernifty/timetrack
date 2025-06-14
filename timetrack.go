package main

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/jessevdk/go-flags"
	_ "github.com/mattn/go-sqlite3"
)

var (
	// SKIP          = map[string]struct{}{"loginwindow": {}, "ScreenSaverEngine": {}}
	SKIP         = []string{"loginwindow", "ScreenSaverEngine"}
	WAIT         = 10
	UPDATE_MINOR = 600
	UPDATE_MAJOR = 3600
	SCHEMA       = `
		create table if not exists history (
			occur text, -- yyyy-mm-dd
			category text, -- day week
			app text, -- name of text
			time int -- in minutes
		);

		create table if not exists current (
			category text,
			app text,
			count int -- in segments
		)
	`
)

func initDB(db string) {
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

func notify(msg string) {
	log.Println(msg) // TODO: Implement actual notification
}

func process(current string, counts map[string]map[string]int) {
	counts["day"][current]++
	counts["week"][current]++

	// 10 minute intervals for day
	seconds := counts["day"][current] * WAIT
	if seconds < UPDATE_MAJOR {
		if seconds%UPDATE_MINOR == 0 {
			notify(fmt.Sprintf("%d minutes today: %s", seconds/60, current))
		}
	} else if seconds%UPDATE_MAJOR == 0 {
		notify(fmt.Sprintf("%d hour(s) today: %s", seconds/3600, current))
	}

	// same for week but only hours
	seconds = counts["week"][current] * WAIT
	if seconds >= UPDATE_MAJOR && seconds%UPDATE_MAJOR == 0 {
		notify(fmt.Sprintf("%d hour(s) this week: %s", seconds/3600, current))
	}
}

func mainLoop(db string) {
	log.Printf("writing to %s...", db)
	counts := map[string]map[string]int{
		"day":  make(map[string]int),
		"week": make(map[string]int),
	}

	lastTime, _ := load(counts, db) // TODO handle err
	if lastTime.IsZero() {
		lastTime = time.Now()
	}

	count := 0
	for {
		count++
		// log.Printf("count is %d", count)

		newTime := time.Now()
		if isNewDay(lastTime, newTime) {
			write("day", counts, lastTime, db)   // save
			counts["day"] = make(map[string]int) // reset
			lastTime = newTime
		}
		if isNewWeek(lastTime, newTime) {
			write("week", counts, lastTime, db)   // save
			counts["week"] = make(map[string]int) // reset
			lastTime = newTime
		}

		current, _ := currentApp() // TODO handle err

		// skip if not interested
		if !contains(SKIP, current) {
			process(current, counts) // update counts
			if count%6 == 0 {        // every minute
				save(counts, lastTime, db)
			}
		}

		time.Sleep(time.Duration(WAIT) * time.Second)
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

func dbFile() string {
	configDir, err := os.UserConfigDir()
	if err != nil {
		panic(err)
	}

	appConfigDir := filepath.Join(configDir, "TimeTrack")
	fmt.Println("Config directory:", appConfigDir)
	return filepath.Join(appConfigDir, "timetrack.sqlite")
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

	initDB(opts.DB)
	mainLoop(opts.DB)
}
