package webrtcing

import (
  "src/faker"
  "appengine"
  "appengine/channel"
  "appengine/datastore"
  "encoding/json"
  "math/rand"
  "net/http"
  "text/template"
  "strings"
  "io/ioutil"
  "time"
  "os"
)

type Template struct {
  Room string
  User string
  Token string
  AcceptLanguage string
  Minified string
}

type Room struct {
  User1 string
  User2 string
  Connected1 bool
  Connected2 bool
}

func (r *Room) OtherUser(user string) string {
  if user == r.User2 {
    return r.User1
  }
  if user == r.User1 {
    return r.User2
  }
  return ""
}

func (r *Room) AddUser(user string) {
  if r.User1 == "" {
    r.User1 = user
  } else if r.User2 == "" {
    r.User2 = user
  }
}

func (r *Room) RemoveUser(user string) bool {
  if user == r.User2 {
    r.User2 = ""
    r.Connected2 = false
  }
  if user == r.User1 {
    r.User1 = ""
    r.Connected1 = false
  }
  // returns true if it should be deleted
  return r.Occupants() == 0
}

func (r *Room) ConnectUser(user string) {
  if user == r.User1 {
    r.Connected1 = true
  }
  if user == r.User2 {
    r.Connected2 = true
  }
}

func (r *Room) Occupants() int {
  occupancy := 0
  if r.User1 != "" { occupancy += 1 }
  if r.User2 != "" { occupancy += 1 }
  return occupancy
}

func GetRoom(c appengine.Context, name string) (*Room, error) {
  k := datastore.NewKey(c, "Room", name, 0, nil)
  r := new(Room)
  err := datastore.Get(c, k, r)
  return r, err;
}

func PutRoom(c appengine.Context, name string, room *Room) error {
  k := datastore.NewKey(c, "Room", name, 0, nil)
  _, err := datastore.Put(c, k, room)
  return err;
}

func DelRoom(c appengine.Context, name string) error {
  k := datastore.NewKey(c, "Room", name, 0, nil)
  err := datastore.Delete(c, k)
  return err;
}



func Main(w http.ResponseWriter, r *http.Request) {
  c := appengine.NewContext(r)
  w.Header().Set("Content-Type", "text/html; charset=utf-8")

  // redirect to room name
  if r.URL.Path == "/" {
    if fake, err := faker.New("en"); err == nil {
      // TODO make sure the room doesn't exist...
      http.Redirect(w, r, "/"+fake.DomainWord(), 302);
    } else {
      c.Criticalf("execution failed: %s", err)
    }
    return;
  }

  roomName := strings.TrimLeft(r.URL.Path,"/")
  userName := Random(10)

  room, err := GetRoom(c, roomName)

  // Empty room
  if err != nil {
    c.Debugf("%v",err)
    room := new(Room)
    room.AddUser(userName)
    c.Debugf("Created room %s",roomName)
    if err := PutRoom(c, roomName, room); err != nil {
      c.Criticalf("could not save room: %s", err)
      return;
    }

  // Join room
  } else if room.Occupants() == 1 {
    room.AddUser(userName)
    c.Debugf("Joined room %s",roomName)
    if err := PutRoom(c, roomName, room); err != nil {
      c.Criticalf("could not save room: %s", err)
      return;
    }

  // Full room
  } else if room.Occupants() == 2 {
    c.Debugf("Room: %v",room)
    c.Criticalf("Room full %s",roomName)
    return;

  // DataStore error
  } else if err != nil {
    c.Criticalf("Error occured while getting room %s",roomName,err)
    return;
  }

  // Accept-Language:
  acceptLanguage := "en"
  var header map[string][]string;
  header = r.Header
  if _,ok := header["Accept-Language"]; ok {
    acceptLanguage = strings.Join(header["Accept-Language"], ",")
  }

  // Is minified js newer?
  // TODO there must be a better way?!
  minified := ""
  if mi, err := os.Stat("build/build.min.js"); err == nil {
    if bi, err := os.Stat("build/build.js"); err == nil {
      if mi.ModTime().Unix() > bi.ModTime().Unix() {
        minified = "min."
      }
    }
  }

  // Create a Channel token
  token, err := channel.Create(c, MakeClientId(roomName, userName))
  if err != nil {
    http.Error(w, "Couldn't create Channel", http.StatusInternalServerError)
    return
  }

  // Data to be sent to the template:
  data := Template{Room:roomName, User: userName, Token: token, AcceptLanguage: acceptLanguage, Minified: minified}

  // Parse the template and output HTML:
  template, err := template.ParseFiles("build/build.html")
  if err != nil { c.Criticalf("execution failed: %s", err) }
  err = template.Execute(w, data)
  if err != nil { c.Criticalf("execution failed: %s", err) }

}


func Connected(w http.ResponseWriter, r *http.Request) {
  c := appengine.NewContext(r)
  roomName, userName := ParseClientId(r.FormValue("from"))
  if room, err := GetRoom(c, roomName); err == nil {
    room.ConnectUser(userName)
    c.Debugf("Connected user %s to room %s",userName,roomName)

    err := PutRoom(c, roomName, room)
    if err == nil {
      // let the other user know
      otherUser := room.OtherUser(userName)
      if err := channel.Send(c, MakeClientId(roomName, otherUser), "connected"); err != nil {
        c.Criticalf("Error while sending connected:",err)
      }
      if err := channel.Send(c, MakeClientId(roomName, userName), "connected"); err != nil {
        c.Criticalf("Error while sending connected:",err)
      }
    } else {
      c.Criticalf("Could not put room %s: ",roomName,err)
    }
  } else {
    c.Criticalf("Could not get room %s: ",roomName,err)
  }
}

func Disconnected(w http.ResponseWriter, r *http.Request) {
  c := appengine.NewContext(r)
  roomName, userName := ParseClientId(r.FormValue("from"))
  if room, err := GetRoom(c, roomName); err == nil {
    empty := room.RemoveUser(userName)
    c.Debugf("Removed user %s from room %s",userName,roomName)

    // delete empty rooms
    if empty {
      err := DelRoom(c, roomName)
      if err != nil {
        c.Criticalf("Could not del room %s: ",roomName,err)
      } else {
        // respond with success
      }

    // save room if not empty
    } else {
      err := PutRoom(c, roomName, room)
      if err != nil {
        c.Criticalf("Could not put room %s: ",roomName,err)
      } else {
        // let the other user know
        otherUser := room.OtherUser(userName)
        if err := channel.Send(c, MakeClientId(roomName, otherUser), "disconnected"); err != nil {
          c.Criticalf("Error while sending disconnected:",err)
        }
        if err := channel.Send(c, MakeClientId(roomName, userName), "disconnected"); err != nil {
          c.Criticalf("Error while sending disconnected:",err)
        }
      }
    }
  } else {
    c.Criticalf("Could not get room %s: ",roomName,err)
  }
}

func OnMessage(w http.ResponseWriter, r *http.Request) {
  c := appengine.NewContext(r)

  roomName, userName := ParseClientId(r.FormValue("from"))

  b, err := ioutil.ReadAll(r.Body);
  if err != nil {
    c.Criticalf("%s",err)
    return
  }
  r.Body.Close()

  msg, err := ReadData(b)
  if err != nil {
    c.Criticalf("Error reading JSON",err)
    return
  }

  c.Debugf("received channel data message: %s",b)

  room, err := GetRoom(c, roomName)
  if err != nil {
    c.Criticalf("Error while retreiving room:",err)
  }
  otherUser := room.OtherUser(userName)
  if err := channel.SendJSON(c, MakeClientId(roomName, otherUser), msg); err != nil {
    c.Criticalf("Error while sending JSON:",err)
  }

  w.Write([]byte("OK"))
}

func MakeClientId(room string, user string) string {
  return user + "@" + room;
}

func ParseClientId(clientId string) (string, string) {
  from := strings.Split(clientId, "@")
  // room, user
  return from[1], from[0]
}

func Random(length int) string {
  // only upper case because the link will be upper case when copied
  printables := "ABCDEFGHIJKLMNOPQRSTUVWXYX0123456789"
  result := ""
  for i := 0; i < length; i++ {
    pos := rand.Intn(len(printables) - 1)
    result = result + printables[pos:pos + 1]
  }
  return result
}

func ReadData(d []byte) (interface{}, error) {
  var data interface{}
  if err := json.Unmarshal(d, &data); err != nil {
    return data, err
  }
  return data, nil
}


func init() {
  now := time.Now()
  rand.Seed(now.Unix())
  http.HandleFunc("/", Main)
  http.HandleFunc("/message", OnMessage)
  http.HandleFunc("/disconnect", Disconnected)
  http.HandleFunc("/_ah/channel/connected/", Connected)
  http.HandleFunc("/_ah/channel/disconnected/", Disconnected)
}