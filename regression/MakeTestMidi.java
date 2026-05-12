import javax.sound.midi.*;
import java.io.File;
import java.io.IOException;

/**
 * Generates deterministic MIDI fixtures used to render reference WAVs.
 *
 *   javac MakeTestMidi.java
 *   java  MakeTestMidi <output-dir>
 *
 * Writes:
 *   <dir>/scale.mid   - monophonic C major scale, 8 quarter-notes
 *   <dir>/chord.mid   - monophonic intro + C-E-G chord + sustained A
 */
public class MakeTestMidi {

    static final int PPQ = 480;

    public static void main(String[] args) throws Exception {
        if (args.length < 1) {
            System.err.println("usage: MakeTestMidi <output-dir>");
            System.exit(2);
        }
        File dir = new File(args[0]);
        if (!dir.isDirectory()) {
            System.err.println("not a directory: " + dir);
            System.exit(2);
        }
        writeScale(new File(dir, "scale.mid"));
        writeChord(new File(dir, "chord.mid"));
    }

    static void writeScale(File out) throws Exception {
        Sequence seq = new Sequence(Sequence.PPQ, PPQ);
        Track t = seq.createTrack();
        int[] notes = {60, 62, 64, 65, 67, 69, 71, 72};
        long tick = 0;
        for (int n : notes) {
            addNote(t, 0, n, 100, tick, PPQ - 20);
            tick += PPQ;
        }
        MidiSystem.write(seq, 1, out);
        System.out.println("wrote " + out + " (" + tick + " ticks)");
    }

    static void writeChord(File out) throws Exception {
        Sequence seq = new Sequence(Sequence.PPQ, PPQ);
        Track t = seq.createTrack();
        long tick = 0;
        // Three arpeggio notes
        for (int n : new int[]{60, 64, 67}) {
            addNote(t, 0, n, 100, tick, PPQ - 20);
            tick += PPQ;
        }
        // C-E-G held together for 3 quarter-notes
        for (int n : new int[]{60, 64, 67}) {
            addNote(t, 0, n, 100, tick, 3*PPQ - 20);
        }
        tick += 3*PPQ;
        // Sustained A for 4 quarter-notes (envelope decay test)
        addNote(t, 0, 69, 100, tick, 4*PPQ - 20);
        tick += 4*PPQ;
        MidiSystem.write(seq, 1, out);
        System.out.println("wrote " + out + " (" + tick + " ticks)");
    }

    static void addNote(Track t, int chan, int note, int vel, long tick, int dur)
            throws InvalidMidiDataException {
        ShortMessage on = new ShortMessage();
        on.setMessage(ShortMessage.NOTE_ON, chan, note, vel);
        t.add(new MidiEvent(on, tick));
        ShortMessage off = new ShortMessage();
        off.setMessage(ShortMessage.NOTE_OFF, chan, note, 0);
        t.add(new MidiEvent(off, tick + dur));
    }
}
